// Package farfield provides an idiomatic HTTP client for Farfield History and
// Runtime. Calls are durable by default: a successful mutation means the
// Farfield server acknowledged the authoritative object-store commit.
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
	Since          *time.Time
	Limit          int
}

type RuntimeStatus string

const (
	Queued    RuntimeStatus = "queued"
	Running   RuntimeStatus = "running"
	Waiting   RuntimeStatus = "waiting"
	Sleeping  RuntimeStatus = "sleeping"
	Completed RuntimeStatus = "completed"
	Failed    RuntimeStatus = "failed"
	Cancelled RuntimeStatus = "cancelled"
	Ambiguous RuntimeStatus = "ambiguous"
)

type RuntimeEvent struct {
	SchemaVersion       string          `json:"schema_version"`
	ID                  string          `json:"id"`
	RunID               string          `json:"run_id"`
	OperationID         string          `json:"operation_id"`
	Sequence            uint64          `json:"sequence"`
	Attempt             uint32          `json:"attempt"`
	Kind                string          `json:"kind"`
	From                *RuntimeStatus  `json:"from"`
	To                  RuntimeStatus   `json:"to"`
	OccurredAt          time.Time       `json:"occurred_at"`
	RecordedAt          time.Time       `json:"recorded_at"`
	Checkpoint          json.RawMessage `json:"checkpoint,omitempty"`
	PreviousEventSHA256 string          `json:"previous_event_sha256,omitempty"`
	EventSHA256         string          `json:"event_sha256"`
}

type Run struct {
	ID              string          `json:"id"`
	Status          RuntimeStatus   `json:"status"`
	Sequence        uint64          `json:"sequence"`
	Attempt         uint32          `json:"attempt"`
	UpdatedAt       time.Time       `json:"updated_at"`
	LastEventID     string          `json:"last_event_id"`
	LastEventSHA256 string          `json:"last_event_sha256"`
	Checkpoint      json.RawMessage `json:"checkpoint,omitempty"`
	CheckpointAt    *time.Time      `json:"checkpoint_at,omitempty"`
}

type CreateRunInput struct {
	ID          string `json:"id,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
	Checkpoint  any    `json:"checkpoint,omitempty"`
}

type TransitionRunInput struct {
	OperationID string        `json:"operation_id,omitempty"`
	To          RuntimeStatus `json:"to"`
	Checkpoint  any           `json:"checkpoint,omitempty"`
}

type CheckpointRunInput struct {
	OperationID string `json:"operation_id,omitempty"`
	Checkpoint  any    `json:"checkpoint"`
}
