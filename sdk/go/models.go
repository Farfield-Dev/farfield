// Package farfield provides an idiomatic HTTP client for Farfield History.
// Calls are durable by default: a successful mutation means the Farfield
// server acknowledged the authoritative object-store commit.
package farfield

import (
	"encoding/json"
	"time"
)

const Version = "0.1.0-alpha.1"

type CaptureInput struct {
	ID             string            `json:"id,omitempty"`
	ConversationID string            `json:"conversation_id,omitempty"`
	Kind           string            `json:"kind"`
	Content        any               `json:"content"`
	OccurredAt     *time.Time        `json:"occurred_at,omitempty"`
	Sequence       *uint64           `json:"sequence,omitempty"`
	TraceID        string            `json:"trace_id,omitempty"`
	SpanID         string            `json:"span_id,omitempty"`
	ParentID       string            `json:"parent_id,omitempty"`
	Agent          string            `json:"agent,omitempty"`
	Tool           string            `json:"tool,omitempty"`
	Status         string            `json:"status,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
}

type BatchInput struct {
	ID      string         `json:"id,omitempty"`
	Records []CaptureInput `json:"records"`
}

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
	RecordSHA256   string            `json:"record_sha256"`
}

type SegmentEntry struct {
	Record  Record          `json:"record"`
	Content json.RawMessage `json:"content,omitempty"`
}

type Segment struct {
	SchemaVersion    string         `json:"schema_version"`
	ID               string         `json:"id"`
	ConversationID   string         `json:"conversation_id"`
	ConversationHash string         `json:"conversation_hash"`
	CreatedAt        time.Time      `json:"created_at"`
	Entries          []SegmentEntry `json:"entries"`
	SegmentSHA256    string         `json:"segment_sha256"`
}

type Entry struct {
	Record  Record          `json:"record"`
	Content json.RawMessage `json:"content"`
}

type ConversationSummary struct {
	ID          string    `json:"id"`
	RecordCount int       `json:"record_count"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	Agents      []string  `json:"agents"`
	Kinds       []string  `json:"kinds"`
}

type HistoryQuery struct {
	ConversationID string
	TraceID        string
	Kind           string
	Agent          string
	Tool           string
	Status         string
	Tags           map[string]string
	Since          *time.Time
	Until          *time.Time
	Limit          int
}

// SearchQuery supports ranked content search plus exact metadata filters.
// Quoted text is a phrase and a trailing * performs prefix matching.
type SearchQuery struct {
	Text           string
	ConversationID string
	TraceID        string
	Kind           string
	Agent          string
	Tool           string
	Status         string
	Tags           map[string]string
	Since          *time.Time
	Until          *time.Time
	Limit          int
}

type SearchHit struct {
	Record  Record  `json:"record"`
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet,omitempty"`
}

type SearchResult struct {
	Hits           []SearchHit `json:"hits"`
	Total          int         `json:"total"`
	TookMS         float64     `json:"took_ms"`
	IndexedRecords int         `json:"indexed_records"`
	IndexUpdatedAt time.Time   `json:"index_updated_at"`
}
