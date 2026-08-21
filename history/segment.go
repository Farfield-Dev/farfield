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
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/Farfield-Dev/farfield/internal/canonicaljson"
	"github.com/Farfield-Dev/farfield/internal/identity"
	"github.com/Farfield-Dev/farfield/storage"
)

const (
	SegmentSchema            = "farfield.history.segment.v1"
	DefaultMaxSegmentRecords = 1_000
	DefaultMaxSegmentBytes   = 16 * 1024 * 1024
	historySegmentsPrefix    = "history/v2/conversations"
	historyBlobPrefix        = "history/v2/blobs/sha256"
	segmentReadConcurrency   = 16
)

// Segment is the durable batch unit for high-volume history ingestion. Small
// JSON content is stored inline with its record envelope so one object commit
// can make many logical records durable.
type Segment struct {
	SchemaVersion    string         `json:"schema_version"`
	ID               string         `json:"id"`
	ConversationID   string         `json:"conversation_id"`
	ConversationHash string         `json:"conversation_hash"`
	CreatedAt        time.Time      `json:"created_at"`
	Entries          []SegmentEntry `json:"entries"`
	SegmentSHA256    string         `json:"segment_sha256,omitempty"`
}

type SegmentEntry struct {
	Record  Record          `json:"record"`
	Content json.RawMessage `json:"content,omitempty"`
}

type AppendBatchInput struct {
	SegmentID string
	Records   []AppendInput
}

func (service *Service) AppendBatch(ctx context.Context, input AppendBatchInput) (Segment, error) {
	if len(input.Records) == 0 {
		return Segment{}, failure("FH_INVALID_BATCH", "a segment requires at least one record", nil)
	}
	if len(input.Records) > service.maxSegmentRecords {
		return Segment{}, failure("FH_SEGMENT_TOO_LARGE", fmt.Sprintf("segment contains %d records; limit is %d", len(input.Records), service.maxSegmentRecords), nil)
	}
	conversationID := input.Records[0].ConversationID
	if !validID.MatchString(conversationID) {
		return Segment{}, failure("FH_INVALID_BATCH", "conversation id is invalid", nil)
	}
	for _, record := range input.Records[1:] {
		if record.ConversationID != conversationID {
			return Segment{}, failure("FH_INVALID_BATCH", "all records in a segment must belong to one conversation", nil)
		}
	}

	segmentID := input.SegmentID
	var err error
	if segmentID == "" {
		segmentID, err = identity.New("seg_")
		if err != nil {
			return Segment{}, failure("FH_ID_GENERATION_FAILED", "segment id could not be generated", err)
		}
	}
	if !validID.MatchString(segmentID) {
		return Segment{}, failure("FH_INVALID_BATCH", "segment id is invalid", nil)
	}

	key := segmentKey(segmentID, conversationID)
	now := service.now().UTC()
	entries := make([]SegmentEntry, 0, len(input.Records))
	type blobWrite struct {
		key  string
		data []byte
	}
	blobs := make([]blobWrite, 0)
	contentBytes := 0
	for index, value := range input.Records {
		content, normalizeErr := canonicaljson.Normalize(value.Content)
		if normalizeErr != nil {
			return Segment{}, failure("FH_INVALID_JSON", fmt.Sprintf("record %d content is not valid JSON", index), normalizeErr)
		}
		if len(content) > service.maxContentBytes {
			return Segment{}, failure("FH_CONTENT_TOO_LARGE", fmt.Sprintf("record %d content is %d bytes; limit is %d", index, len(content), service.maxContentBytes), nil)
		}
		recordID := value.RecordID
		if recordID == "" {
			recordID, err = identity.New("rec_")
			if err != nil {
				return Segment{}, failure("FH_ID_GENERATION_FAILED", "record id could not be generated", err)
			}
		}
		occurredAt := now
		if value.OccurredAt != nil {
			occurredAt = value.OccurredAt.UTC()
		}
		digest := sha256.Sum256(content)
		contentSHA := hex.EncodeToString(digest[:])
		entryIndex := index
		contentRef := ContentRef{
			SHA256: contentSHA, Size: len(content), MediaType: "application/json",
		}
		var inline json.RawMessage
		if len(content) <= service.maxInlineContentBytes {
			contentBytes += len(content)
			if contentBytes > service.maxSegmentBytes {
				return Segment{}, failure("FH_SEGMENT_TOO_LARGE", fmt.Sprintf("inline segment content is %d bytes; limit is %d", contentBytes, service.maxSegmentBytes), nil)
			}
			contentRef.Key = key
			contentRef.Storage = "segment"
			contentRef.EntryIndex = &entryIndex
			inline = json.RawMessage(content)
		} else {
			contentRef.Key = fmt.Sprintf("%s/%s/%s", historyBlobPrefix, contentSHA[:2], contentSHA[2:])
			contentRef.Storage = "blob"
			blobs = append(blobs, blobWrite{key: contentRef.Key, data: content})
		}
		record, sealErr := (Record{
			SchemaVersion: RecordSchema, ID: recordID,
			ConversationID: conversationID, Kind: value.Kind,
			OccurredAt: occurredAt, RecordedAt: now, Sequence: value.Sequence,
			TraceID: value.TraceID, SpanID: value.SpanID, ParentID: value.ParentID,
			Agent: value.Agent, Tool: value.Tool, Status: value.Status,
			Tags:    cloneTags(value.Tags),
			Content: contentRef,
		}).Seal()
		if sealErr != nil {
			return Segment{}, sealErr
		}
		entries = append(entries, SegmentEntry{Record: record, Content: inline})
	}

	segment, err := (Segment{
		SchemaVersion: SegmentSchema, ID: segmentID,
		ConversationID: conversationID, ConversationHash: conversationHash(conversationID),
		CreatedAt: now, Entries: entries,
	}).Seal()
	if err != nil {
		return Segment{}, err
	}
	encoded, err := canonicaljson.Marshal(segment)
	if err != nil {
		return Segment{}, failure("FH_INVALID_SEGMENT", "segment cannot be encoded", err)
	}
	if len(encoded) > service.maxSegmentBytes {
		return Segment{}, failure("FH_SEGMENT_TOO_LARGE", fmt.Sprintf("encoded segment is %d bytes; limit is %d", len(encoded), service.maxSegmentBytes), nil)
	}
	for _, blob := range blobs {
		if _, err := service.store.PutIfAbsent(ctx, blob.key, blob.data, storage.PutOptions{ContentType: "application/json"}); err != nil {
			return Segment{}, failure("FH_BLOB_WRITE_FAILED", "segment content could not be committed", err)
		}
	}
	if _, err := service.store.PutIfAbsent(ctx, key, encoded, storage.PutOptions{ContentType: "application/json"}); err != nil {
		if !errors.Is(err, storage.ErrConflict) {
			return Segment{}, failure("FH_SEGMENT_WRITE_FAILED", "segment could not be committed", err)
		}
		existing, readErr := service.readSegmentAt(ctx, key)
		if readErr != nil {
			return Segment{}, readErr
		}
		if !sameBatch(existing, input) {
			return Segment{}, failure("FH_IDEMPOTENCY_CONFLICT", fmt.Sprintf("segment id %q was reused for different records", segmentID), err)
		}
		if err := service.projectSource(ctx, key, existing.SegmentSHA256, segmentRecords(existing)); err != nil {
			return Segment{}, err
		}
		return existing, nil
	}
	if err := service.projectSource(ctx, key, segment.SegmentSHA256, segmentRecords(segment)); err != nil {
		return Segment{}, err
	}
	return segment, nil
}

func segmentRecords(segment Segment) []Record {
	records := make([]Record, len(segment.Entries))
	for index, entry := range segment.Entries {
		records[index] = entry.Record
	}
	return records
}

func (segment Segment) Validate() error {
	if segment.SchemaVersion != SegmentSchema {
		return failure("FH_SCHEMA_UNSUPPORTED", fmt.Sprintf("unsupported schema %q", segment.SchemaVersion), nil)
	}
	if !validID.MatchString(segment.ID) || !validID.MatchString(segment.ConversationID) || segment.CreatedAt.IsZero() || len(segment.Entries) == 0 {
		return failure("FH_INVALID_SEGMENT", "segment identity, timestamp, or entries are invalid", nil)
	}
	if segment.ConversationHash != conversationHash(segment.ConversationID) {
		return failure("FH_INVALID_SEGMENT", "segment conversation hash does not match its conversation", nil)
	}
	key := segmentKey(segment.ID, segment.ConversationID)
	recordIDs := make(map[string]struct{}, len(segment.Entries))
	for index, entry := range segment.Entries {
		if err := entry.Record.Verify(); err != nil {
			return err
		}
		if entry.Record.SchemaVersion != RecordSchema || entry.Record.ConversationID != segment.ConversationID {
			return failure("FH_INVALID_SEGMENT", fmt.Sprintf("entry %d does not belong to this segment", index), nil)
		}
		if _, exists := recordIDs[entry.Record.ID]; exists {
			return failure("FH_INVALID_SEGMENT", fmt.Sprintf("record id %q appears more than once in the segment", entry.Record.ID), nil)
		}
		recordIDs[entry.Record.ID] = struct{}{}
		switch entry.Record.Content.Storage {
		case "segment":
			if entry.Record.Content.Key != key || entry.Record.Content.EntryIndex == nil || *entry.Record.Content.EntryIndex != index {
				return failure("FH_INVALID_SEGMENT", fmt.Sprintf("entry %d has an invalid segment locator", index), nil)
			}
			content, err := canonicaljson.Normalize(entry.Content)
			if err != nil || !bytes.Equal(content, entry.Content) {
				return failure("FH_INVALID_SEGMENT", fmt.Sprintf("entry %d content is not canonical JSON", index), err)
			}
			digest := sha256.Sum256(content)
			if hex.EncodeToString(digest[:]) != entry.Record.Content.SHA256 || len(content) != entry.Record.Content.Size {
				return failure("FH_INVALID_SEGMENT", fmt.Sprintf("entry %d content failed its checksum", index), nil)
			}
		case "blob":
			if len(entry.Content) != 0 {
				return failure("FH_INVALID_SEGMENT", fmt.Sprintf("entry %d duplicates external blob content", index), nil)
			}
		}
	}
	return nil
}

func (segment Segment) ComputeHash() (string, error) {
	segment.SegmentSHA256 = ""
	data, err := canonicaljson.Marshal(segment)
	if err != nil {
		return "", failure("FH_INVALID_SEGMENT", "segment cannot be encoded", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func (segment Segment) Seal() (Segment, error) {
	if err := segment.Validate(); err != nil {
		return Segment{}, err
	}
	digest, err := segment.ComputeHash()
	if err != nil {
		return Segment{}, err
	}
	segment.SegmentSHA256 = digest
	return segment, nil
}

func (segment Segment) Verify() error {
	if err := segment.Validate(); err != nil {
		return err
	}
	expected, err := segment.ComputeHash()
	if err != nil {
		return err
	}
	if segment.SegmentSHA256 != expected {
		return failure("FH_SEGMENT_CORRUPT", fmt.Sprintf("segment %q failed its checksum", segment.ID), nil)
	}
	return nil
}

func (service *Service) readSegmentAt(ctx context.Context, key string) (Segment, error) {
	data, err := service.store.Get(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return Segment{}, failure("FH_NOT_FOUND", "segment was not found", err)
	}
	if err != nil {
		return Segment{}, failure("FH_SEGMENT_READ_FAILED", "segment could not be read", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var segment Segment
	if err := decoder.Decode(&segment); err != nil {
		return Segment{}, failure("FH_SEGMENT_CORRUPT", "segment is not valid JSON", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Segment{}, failure("FH_SEGMENT_CORRUPT", "segment contains trailing JSON data", err)
	}
	if err := segment.Verify(); err != nil {
		return Segment{}, err
	}
	if key != segmentKey(segment.ID, segment.ConversationID) {
		return Segment{}, failure("FH_SEGMENT_CORRUPT", "segment is stored under the wrong object key", nil)
	}
	return segment, nil
}

func (service *Service) listSegments(ctx context.Context, prefix string) ([]Segment, error) {
	keys, err := service.store.List(ctx, prefix)
	if err != nil {
		return nil, failure("FH_SEGMENT_LIST_FAILED", "segments could not be listed", err)
	}
	segments := make([]Segment, len(keys))
	jobs := make(chan int)
	workers := min(segmentReadConcurrency, len(keys))
	var wait sync.WaitGroup
	var firstError error
	var errorOnce sync.Once
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range jobs {
				segment, readErr := service.readSegmentAt(ctx, keys[index])
				if readErr != nil {
					errorOnce.Do(func() { firstError = readErr })
					continue
				}
				segments[index] = segment
			}
		}()
	}
	for index := range keys {
		jobs <- index
	}
	close(jobs)
	wait.Wait()
	if firstError != nil {
		return nil, firstError
	}
	sort.Slice(segments, func(left, right int) bool {
		if segments[left].CreatedAt.Equal(segments[right].CreatedAt) {
			return segments[left].ID < segments[right].ID
		}
		return segments[left].CreatedAt.Before(segments[right].CreatedAt)
	})
	return segments, nil
}

func conversationHash(conversationID string) string {
	digest := sha256.Sum256([]byte(conversationID))
	return hex.EncodeToString(digest[:])
}

func conversationSegmentPrefix(conversationID string) string {
	return fmt.Sprintf("%s/%s/segments", historySegmentsPrefix, conversationHash(conversationID))
}

func segmentKey(segmentID, conversationID string) string {
	digest := sha256.Sum256([]byte(conversationID + "\x00" + segmentID))
	value := hex.EncodeToString(digest[:])
	return fmt.Sprintf("%s/%s.json", conversationSegmentPrefix(conversationID), value)
}

func sameBatch(existing Segment, input AppendBatchInput) bool {
	if len(existing.Entries) != len(input.Records) {
		return false
	}
	for index, candidate := range input.Records {
		entry := existing.Entries[index]
		content, err := canonicaljson.Normalize(candidate.Content)
		if err != nil {
			return false
		}
		if entry.Record.Content.Storage == "segment" && !bytes.Equal(content, entry.Content) {
			return false
		}
		digest := sha256.Sum256(content)
		if hex.EncodeToString(digest[:]) != entry.Record.Content.SHA256 || len(content) != entry.Record.Content.Size {
			return false
		}
		if candidate.RecordID != "" && candidate.RecordID != entry.Record.ID {
			return false
		}
		if candidate.OccurredAt != nil && !candidate.OccurredAt.UTC().Equal(entry.Record.OccurredAt) {
			return false
		}
		if candidate.ConversationID != entry.Record.ConversationID || candidate.Kind != entry.Record.Kind || !equalUint64(candidate.Sequence, entry.Record.Sequence) ||
			!equalString(candidate.TraceID, entry.Record.TraceID) || !equalString(candidate.SpanID, entry.Record.SpanID) || !equalString(candidate.ParentID, entry.Record.ParentID) ||
			!equalString(candidate.Agent, entry.Record.Agent) || !equalString(candidate.Tool, entry.Record.Tool) || !equalString(candidate.Status, entry.Record.Status) ||
			!reflect.DeepEqual(cloneTags(candidate.Tags), entry.Record.Tags) {
			return false
		}
	}
	return true
}

func equalString(left, right *string) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

func equalUint64(left, right *uint64) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}
