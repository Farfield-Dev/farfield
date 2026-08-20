// Package history stores immutable evidence from agent conversations.
package history

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Farfield-Dev/farfield/internal/canonicaljson"
)

const (
	RecordSchema   = "farfield.history.record.v1"
	RecordSchemaV2 = "farfield.history.record.v2"
)

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@/-]{0,254}$`)

type ContentRef struct {
	SHA256     string `json:"sha256"`
	Size       int    `json:"size"`
	MediaType  string `json:"media_type"`
	Key        string `json:"key"`
	Storage    string `json:"storage,omitempty"`
	EntryIndex *int   `json:"entry_index,omitempty"`
}

type Record struct {
	SchemaVersion  string            `json:"schema_version"`
	ID             string            `json:"id"`
	ConversationID string            `json:"conversation_id"`
	Kind           string            `json:"kind"`
	OccurredAt     time.Time         `json:"occurred_at"`
	RecordedAt     time.Time         `json:"recorded_at"`
	Sequence       *uint64           `json:"sequence"`
	TraceID        *string           `json:"trace_id"`
	SpanID         *string           `json:"span_id"`
	ParentID       *string           `json:"parent_id"`
	Agent          *string           `json:"agent"`
	Tool           *string           `json:"tool"`
	Status         *string           `json:"status"`
	Tags           map[string]string `json:"tags"`
	Content        ContentRef        `json:"content"`
	RecordSHA256   string            `json:"record_sha256,omitempty"`
}

func (record Record) Validate() error {
	if record.SchemaVersion != RecordSchema && record.SchemaVersion != RecordSchemaV2 {
		return failure("FH_SCHEMA_UNSUPPORTED", fmt.Sprintf("unsupported schema %q", record.SchemaVersion), nil)
	}
	for label, value := range map[string]string{
		"record id": record.ID, "conversation id": record.ConversationID,
	} {
		if !validID.MatchString(value) {
			return failure("FH_INVALID_RECORD", label+" is invalid", nil)
		}
	}
	for label, value := range map[string]*string{
		"trace id": record.TraceID, "span id": record.SpanID, "parent id": record.ParentID,
	} {
		if value != nil && !validID.MatchString(*value) {
			return failure("FH_INVALID_RECORD", label+" is invalid", nil)
		}
	}
	if len(record.Kind) == 0 || len(record.Kind) > 128 {
		return failure("FH_INVALID_RECORD", "kind must contain 1-128 characters", nil)
	}
	if record.OccurredAt.IsZero() || record.RecordedAt.IsZero() {
		return failure("FH_INVALID_RECORD", "timestamps must be present", nil)
	}
	if record.Content.Size < 0 || !validDigest(record.Content.SHA256) || record.Content.MediaType != "application/json" {
		return failure("FH_INVALID_RECORD", "content reference is invalid", nil)
	}
	switch record.SchemaVersion {
	case RecordSchema:
		expectedContentKey := fmt.Sprintf("blobs/v1/sha256/%s/%s", record.Content.SHA256[:2], record.Content.SHA256[2:])
		if record.Content.Key != expectedContentKey || record.Content.Storage != "" || record.Content.EntryIndex != nil {
			return failure("FH_INVALID_RECORD", "v1 content reference is invalid", nil)
		}
	case RecordSchemaV2:
		switch record.Content.Storage {
		case "segment":
			if record.Content.EntryIndex == nil || *record.Content.EntryIndex < 0 || !strings.HasPrefix(record.Content.Key, "segments/v1/shards/") {
				return failure("FH_INVALID_RECORD", "v2 segment content reference is invalid", nil)
			}
		case "blob":
			expectedContentKey := fmt.Sprintf("blobs/v1/sha256/%s/%s", record.Content.SHA256[:2], record.Content.SHA256[2:])
			if record.Content.Key != expectedContentKey || record.Content.EntryIndex != nil {
				return failure("FH_INVALID_RECORD", "v2 blob content reference is invalid", nil)
			}
		default:
			return failure("FH_INVALID_RECORD", "v2 segment content reference is invalid", nil)
		}
	}
	for key, value := range record.Tags {
		if len(key) == 0 || len(key) > 128 || len(value) > 1024 {
			return failure("FH_INVALID_RECORD", "tag key or value is too long", nil)
		}
	}
	return nil
}

func (record Record) ComputeHash() (string, error) {
	record.RecordSHA256 = ""
	data, err := canonicaljson.Marshal(record)
	if err != nil {
		return "", failure("FH_INVALID_RECORD", "record cannot be encoded", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func (record Record) Seal() (Record, error) {
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	digest, err := record.ComputeHash()
	if err != nil {
		return Record{}, err
	}
	record.RecordSHA256 = digest
	return record, nil
}

func (record Record) Verify() error {
	if err := record.Validate(); err != nil {
		return err
	}
	expected, err := record.ComputeHash()
	if err != nil {
		return err
	}
	if record.RecordSHA256 != expected {
		return failure("FH_RECORD_CORRUPT", fmt.Sprintf("record %q failed its checksum", record.ID), nil)
	}
	return nil
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func cloneTags(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		output[key] = input[key]
	}
	return output
}
