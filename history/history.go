package history

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/Farfield-Dev/farfield/internal/canonicaljson"
	"github.com/Farfield-Dev/farfield/internal/identity"
	"github.com/Farfield-Dev/farfield/storage"
)

const DefaultMaxContentBytes = 10 * 1024 * 1024
const DefaultMaxInlineContentBytes = 256 * 1024

type Service struct {
	store                 storage.Store
	maxContentBytes       int
	maxInlineContentBytes int
	maxSegmentBytes       int
	maxSegmentRecords     int
	now                   func() time.Time
}

type Option func(*Service)

func WithMaxContentBytes(limit int) Option {
	return func(service *Service) { service.maxContentBytes = limit }
}

func WithMaxInlineContentBytes(limit int) Option {
	return func(service *Service) { service.maxInlineContentBytes = limit }
}

func WithMaxSegmentBytes(limit int) Option {
	return func(service *Service) { service.maxSegmentBytes = limit }
}

func WithMaxSegmentRecords(limit int) Option {
	return func(service *Service) { service.maxSegmentRecords = limit }
}

func withClock(clock func() time.Time) Option {
	return func(service *Service) { service.now = clock }
}

func New(store storage.Store, options ...Option) (*Service, error) {
	service := &Service{
		store:                 store,
		maxContentBytes:       DefaultMaxContentBytes,
		maxInlineContentBytes: DefaultMaxInlineContentBytes,
		maxSegmentBytes:       DefaultMaxSegmentBytes,
		maxSegmentRecords:     DefaultMaxSegmentRecords,
		now:                   func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		option(service)
	}
	if store == nil || service.maxContentBytes < 1 || service.maxInlineContentBytes < 0 || service.maxInlineContentBytes > service.maxContentBytes || service.maxSegmentBytes < 1 || service.maxSegmentRecords < 1 || service.now == nil {
		return nil, failure("FH_INVALID_CONFIGURATION", "store, clock, and positive content and segment limits are required", nil)
	}
	return service, nil
}

type AppendInput struct {
	RecordID       string
	ConversationID string
	Kind           string
	Content        []byte
	OccurredAt     *time.Time
	Sequence       *uint64
	TraceID        *string
	SpanID         *string
	ParentID       *string
	Agent          *string
	Tool           *string
	Status         *string
	Tags           map[string]string
}

func (service *Service) Append(ctx context.Context, input AppendInput) (Record, error) {
	content, err := canonicaljson.Normalize(input.Content)
	if err != nil {
		return Record{}, failure("FH_INVALID_JSON", "content is not valid JSON", err)
	}
	if len(content) > service.maxContentBytes {
		return Record{}, failure("FH_CONTENT_TOO_LARGE", fmt.Sprintf("content is %d bytes; limit is %d", len(content), service.maxContentBytes), nil)
	}
	contentDigest := sha256.Sum256(content)
	contentSHA := hex.EncodeToString(contentDigest[:])
	contentKey := fmt.Sprintf("blobs/v1/sha256/%s/%s", contentSHA[:2], contentSHA[2:])

	recordID := input.RecordID
	if recordID == "" {
		recordID, err = identity.New("rec_")
		if err != nil {
			return Record{}, failure("FH_ID_GENERATION_FAILED", "record id could not be generated", err)
		}
	}
	now := service.now().UTC()
	occurredAt := now
	if input.OccurredAt != nil {
		occurredAt = input.OccurredAt.UTC()
	}
	record, err := (Record{
		SchemaVersion:  RecordSchema,
		ID:             recordID,
		ConversationID: input.ConversationID,
		Kind:           input.Kind,
		OccurredAt:     occurredAt,
		RecordedAt:     now,
		Sequence:       input.Sequence,
		TraceID:        input.TraceID,
		SpanID:         input.SpanID,
		ParentID:       input.ParentID,
		Agent:          input.Agent,
		Tool:           input.Tool,
		Status:         input.Status,
		Tags:           cloneTags(input.Tags),
		Content: ContentRef{
			SHA256: contentSHA, Size: len(content), MediaType: "application/json", Key: contentKey,
		},
	}).Seal()
	if err != nil {
		return Record{}, err
	}
	// Validate and seal before touching durable storage. The commit order must
	// still be payload first and record second so an acknowledged record can
	// never reference content that was not durably committed.
	if _, err := service.store.PutIfAbsent(ctx, contentKey, content, storage.PutOptions{ContentType: "application/json"}); err != nil {
		return Record{}, failure("FH_BLOB_WRITE_FAILED", "content could not be committed", err)
	}
	encoded, err := canonicaljson.Marshal(record)
	if err != nil {
		return Record{}, failure("FH_INVALID_RECORD", "record cannot be encoded", err)
	}
	key := recordKey(record.ID)
	if _, err := service.store.PutIfAbsent(ctx, key, encoded, storage.PutOptions{ContentType: "application/json"}); err != nil {
		if !errors.Is(err, storage.ErrConflict) {
			return Record{}, failure("FH_RECORD_WRITE_FAILED", "record could not be committed", err)
		}
		existing, readErr := service.readAt(ctx, key)
		if readErr != nil {
			return Record{}, readErr
		}
		if !sameAppend(existing, record, input.OccurredAt != nil) {
			return Record{}, failure("FH_IDEMPOTENCY_CONFLICT", fmt.Sprintf("record id %q was reused for different content", record.ID), err)
		}
		return existing, nil
	}
	return record, nil
}

func (service *Service) ReadRecord(ctx context.Context, recordID string) (Record, error) {
	if !validID.MatchString(recordID) {
		return Record{}, failure("FH_INVALID_RECORD", "record id is invalid", nil)
	}
	record, err := service.readAt(ctx, recordKey(recordID))
	if err == nil {
		return record, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return Record{}, err
	}
	segments, err := service.listSegments(ctx, "segments/v1/shards")
	if err != nil {
		return Record{}, err
	}
	var found *Record
	for _, segment := range segments {
		for _, entry := range segment.Entries {
			if entry.Record.ID != recordID {
				continue
			}
			if found != nil {
				return Record{}, failure("FH_DUPLICATE_RECORD", fmt.Sprintf("record id %q appears in multiple objects", recordID), nil)
			}
			value := entry.Record
			found = &value
		}
	}
	if found == nil {
		return Record{}, failure("FH_NOT_FOUND", "record was not found", storage.ErrNotFound)
	}
	return *found, nil
}

func (service *Service) readAt(ctx context.Context, key string) (Record, error) {
	data, err := service.store.Get(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return Record{}, failure("FH_NOT_FOUND", "record was not found", err)
	}
	if err != nil {
		return Record{}, failure("FH_RECORD_READ_FAILED", "record could not be read", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record Record
	if err := decoder.Decode(&record); err != nil {
		return Record{}, failure("FH_RECORD_CORRUPT", "record is not valid JSON", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Record{}, failure("FH_RECORD_CORRUPT", "record contains trailing JSON data", err)
	}
	if err := record.Verify(); err != nil {
		return Record{}, err
	}
	if key != recordKey(record.ID) {
		return Record{}, failure("FH_RECORD_CORRUPT", "record is stored under the wrong object key", nil)
	}
	return record, nil
}

func (service *Service) ReadContent(ctx context.Context, record Record) ([]byte, error) {
	return service.readContent(ctx, record, nil)
}

func (service *Service) readContent(ctx context.Context, record Record, segments map[string]Segment) ([]byte, error) {
	if err := record.Verify(); err != nil {
		return nil, err
	}
	if record.SchemaVersion == RecordSchemaV2 && record.Content.Storage == "segment" {
		segment, found := segments[record.Content.Key]
		if !found {
			var err error
			segment, err = service.readSegmentAt(ctx, record.Content.Key)
			if err != nil {
				return nil, err
			}
			if segments != nil {
				segments[record.Content.Key] = segment
			}
		}
		if record.Content.EntryIndex == nil || *record.Content.EntryIndex >= len(segment.Entries) {
			return nil, failure("FH_SEGMENT_CORRUPT", "record points outside its segment", nil)
		}
		entry := segment.Entries[*record.Content.EntryIndex]
		if entry.Record.ID != record.ID || entry.Record.RecordSHA256 != record.RecordSHA256 {
			return nil, failure("FH_SEGMENT_CORRUPT", "record does not match its segment entry", nil)
		}
		return append([]byte(nil), entry.Content...), nil
	}
	data, err := service.store.Get(ctx, record.Content.Key)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, failure("FH_BLOB_MISSING", "record content is missing", err)
	}
	if err != nil {
		return nil, failure("FH_BLOB_READ_FAILED", "record content could not be read", err)
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != record.Content.SHA256 {
		return nil, failure("FH_BLOB_CORRUPT", "record content failed its checksum", nil)
	}
	if len(data) != record.Content.Size {
		return nil, failure("FH_BLOB_CORRUPT", "record content size does not match its reference", nil)
	}
	return data, nil
}

func (service *Service) ListRecords(ctx context.Context) ([]Record, error) {
	return service.listRecords(ctx, "segments/v1/shards")
}

func (service *Service) listRecords(ctx context.Context, segmentPrefix string) ([]Record, error) {
	records, _, err := service.listRecordsWithSegments(ctx, segmentPrefix)
	return records, err
}

func (service *Service) listRecordsWithSegments(ctx context.Context, segmentPrefix string) ([]Record, map[string]Segment, error) {
	keys, err := service.store.List(ctx, "records/v1")
	if err != nil {
		return nil, nil, failure("FH_RECORD_LIST_FAILED", "records could not be listed", err)
	}
	records := make([]Record, 0, len(keys))
	for _, key := range keys {
		record, err := service.readAt(ctx, key)
		if err != nil {
			return nil, nil, err
		}
		records = append(records, record)
	}
	segments, err := service.listSegments(ctx, segmentPrefix)
	if err != nil {
		return nil, nil, err
	}
	segmentsByKey := make(map[string]Segment, len(segments))
	for _, segment := range segments {
		segmentsByKey[segmentKey(segment.ID, segment.ConversationID)] = segment
		for _, entry := range segment.Entries {
			records = append(records, entry.Record)
		}
	}
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		if _, exists := seen[record.ID]; exists {
			return nil, nil, failure("FH_DUPLICATE_RECORD", fmt.Sprintf("record id %q appears in multiple objects", record.ID), nil)
		}
		seen[record.ID] = struct{}{}
	}
	sort.Slice(records, func(left, right int) bool {
		if records[left].OccurredAt.Equal(records[right].OccurredAt) {
			if records[left].Sequence != nil && records[right].Sequence != nil && *records[left].Sequence != *records[right].Sequence {
				return *records[left].Sequence < *records[right].Sequence
			}
			return records[left].ID < records[right].ID
		}
		return records[left].OccurredAt.Before(records[right].OccurredAt)
	})
	return records, segmentsByKey, nil
}

type VerificationIssue struct {
	Key   string `json:"key"`
	Error string `json:"error"`
}

type Verification struct {
	OK          bool                `json:"ok"`
	Store       string              `json:"store"`
	Records     int                 `json:"records"`
	Segments    int                 `json:"segments"`
	Blobs       int                 `json:"blobs"`
	OrphanBlobs []string            `json:"orphan_blobs"`
	Issues      []VerificationIssue `json:"issues"`
}

func (service *Service) Verify(ctx context.Context) (Verification, error) {
	recordKeys, err := service.store.List(ctx, "records/v1")
	if err != nil {
		return Verification{}, failure("FH_VERIFY_FAILED", "records could not be listed", err)
	}
	blobKeys, err := service.store.List(ctx, "blobs/v1")
	if err != nil {
		return Verification{}, failure("FH_VERIFY_FAILED", "blobs could not be listed", err)
	}
	segmentKeys, err := service.store.List(ctx, "segments/v1/shards")
	if err != nil {
		return Verification{}, failure("FH_VERIFY_FAILED", "segments could not be listed", err)
	}
	referenced := make(map[string]struct{})
	seenRecords := make(map[string]string)
	result := Verification{Store: service.store.Description(), Segments: len(segmentKeys), Blobs: len(blobKeys), Issues: []VerificationIssue{}, OrphanBlobs: []string{}}
	for _, key := range recordKeys {
		record, readErr := service.readAt(ctx, key)
		if readErr != nil {
			result.Issues = append(result.Issues, VerificationIssue{Key: key, Error: readErr.Error()})
			continue
		}
		result.Records++
		if previous, exists := seenRecords[record.ID]; exists {
			result.Issues = append(result.Issues, VerificationIssue{Key: key, Error: fmt.Sprintf("record id %q also appears in %s", record.ID, previous)})
		} else {
			seenRecords[record.ID] = key
		}
		referenced[record.Content.Key] = struct{}{}
		if _, readErr := service.ReadContent(ctx, record); readErr != nil {
			result.Issues = append(result.Issues, VerificationIssue{Key: record.Content.Key, Error: readErr.Error()})
		}
	}
	for _, key := range segmentKeys {
		segment, readErr := service.readSegmentAt(ctx, key)
		if readErr != nil {
			result.Issues = append(result.Issues, VerificationIssue{Key: key, Error: readErr.Error()})
			continue
		}
		result.Records += len(segment.Entries)
		for _, entry := range segment.Entries {
			if previous, exists := seenRecords[entry.Record.ID]; exists {
				result.Issues = append(result.Issues, VerificationIssue{Key: key, Error: fmt.Sprintf("record id %q also appears in %s", entry.Record.ID, previous)})
			} else {
				seenRecords[entry.Record.ID] = key
			}
			if entry.Record.Content.Storage == "blob" {
				referenced[entry.Record.Content.Key] = struct{}{}
				if _, readErr := service.ReadContent(ctx, entry.Record); readErr != nil {
					result.Issues = append(result.Issues, VerificationIssue{Key: entry.Record.Content.Key, Error: readErr.Error()})
				}
			}
		}
	}
	for _, key := range blobKeys {
		if _, exists := referenced[key]; !exists {
			result.OrphanBlobs = append(result.OrphanBlobs, key)
		}
	}
	result.OK = len(result.Issues) == 0
	return result, nil
}

func recordKey(recordID string) string {
	digest := sha256.Sum256([]byte(recordID))
	value := hex.EncodeToString(digest[:])
	return fmt.Sprintf("records/v1/by-id/%s/%s.json", value[:2], value)
}

func sameAppend(existing, candidate Record, occurredAtWasProvided bool) bool {
	existing.RecordedAt = time.Time{}
	existing.RecordSHA256 = ""
	candidate.RecordedAt = time.Time{}
	candidate.RecordSHA256 = ""
	if !occurredAtWasProvided {
		existing.OccurredAt = time.Time{}
		candidate.OccurredAt = time.Time{}
	}
	left, leftErr := canonicaljson.Marshal(existing)
	right, rightErr := canonicaljson.Marshal(candidate)
	return leftErr == nil && rightErr == nil && string(left) == string(right)
}
