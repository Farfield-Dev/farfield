package history

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

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
	projection            *conversationProjection
	search                *searchProjection
	searchCachePath       string
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

// WithSearchCache stores the disposable full-text index at path. The cache is
// never authoritative and is rebuilt from immutable History when missing or
// corrupt. An empty path keeps the index in memory only.
func WithSearchCache(path string) Option {
	return func(service *Service) { service.searchCachePath = path }
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
	service.projection = newConversationProjection(store, service.now)
	service.search = newSearchProjection(service, service.searchCachePath, service.now)
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
	recordID := input.RecordID
	var err error
	if recordID == "" {
		recordID, err = identity.New("rec_")
		if err != nil {
			return Record{}, failure("FH_ID_GENERATION_FAILED", "record id could not be generated", err)
		}
	}
	input.RecordID = recordID
	digest := sha256.Sum256([]byte(recordID))
	segmentID := "single_" + hex.EncodeToString(digest[:])
	segment, err := service.AppendBatch(ctx, AppendBatchInput{SegmentID: segmentID, Records: []AppendInput{input}})
	if err != nil {
		var domainError *Error
		if errors.As(err, &domainError) && domainError.Code == "FH_IDEMPOTENCY_CONFLICT" {
			return Record{}, failure("FH_IDEMPOTENCY_CONFLICT", fmt.Sprintf("record id %q was reused for different content", recordID), err)
		}
		return Record{}, err
	}
	return segment.Entries[0].Record, nil
}

func (service *Service) ReadRecord(ctx context.Context, recordID string) (Record, error) {
	if !validID.MatchString(recordID) {
		return Record{}, failure("FH_INVALID_RECORD", "record id is invalid", nil)
	}
	segments, err := service.listSegments(ctx, historySegmentsPrefix)
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

func (service *Service) ReadContent(ctx context.Context, record Record) ([]byte, error) {
	return service.readContent(ctx, record, nil)
}

func (service *Service) readContent(ctx context.Context, record Record, segments map[string]Segment) ([]byte, error) {
	if err := record.Verify(); err != nil {
		return nil, err
	}
	if record.Content.Storage == "segment" {
		segment, found := segments[record.Content.Key]
		if !found {
			if segments != nil {
				return nil, failure("FH_SEGMENT_CORRUPT", "record references a segment outside the loaded conversation", nil)
			}
			var err error
			segment, err = service.readSegmentAt(ctx, record.Content.Key)
			if err != nil {
				return nil, err
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
	return service.listRecords(ctx, historySegmentsPrefix)
}

func (service *Service) listRecords(ctx context.Context, segmentPrefix string) ([]Record, error) {
	records, _, err := service.listRecordsWithSegments(ctx, segmentPrefix)
	return records, err
}

func (service *Service) listRecordsWithSegments(ctx context.Context, segmentPrefix string) ([]Record, map[string]Segment, error) {
	segments, err := service.listSegments(ctx, segmentPrefix)
	if err != nil {
		return nil, nil, err
	}
	segmentsByKey := make(map[string]Segment, len(segments))
	records := make([]Record, 0)
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
	blobKeys, err := service.store.List(ctx, historyBlobPrefix)
	if err != nil {
		return Verification{}, failure("FH_VERIFY_FAILED", "blobs could not be listed", err)
	}
	segmentKeys, err := service.store.List(ctx, historySegmentsPrefix)
	if err != nil {
		return Verification{}, failure("FH_VERIFY_FAILED", "segments could not be listed", err)
	}
	referenced := make(map[string]struct{})
	seenRecords := make(map[string]string)
	result := Verification{Store: service.store.Description(), Segments: len(segmentKeys), Blobs: len(blobKeys), Issues: []VerificationIssue{}, OrphanBlobs: []string{}}
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
