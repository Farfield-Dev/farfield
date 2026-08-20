package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Farfield-Dev/farfield/internal/canonicaljson"
	"github.com/Farfield-Dev/farfield/internal/identity"
	"github.com/Farfield-Dev/farfield/storage"
)

const DefaultMaxCheckpointBytes = 1024 * 1024

const (
	EventRunCreated = "run.created"
	EventTransition = "run.transition"
	EventCheckpoint = "run.checkpoint"
)

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@/-]{0,254}$`)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Cause   error  `json:"-"`
}

func (err *Error) Error() string {
	if err.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", err.Code, err.Message, err.Cause)
	}
	return fmt.Sprintf("%s: %s", err.Code, err.Message)
}

func (err *Error) Unwrap() error { return err.Cause }

func failure(code, message string, cause error) error {
	return &Error{Code: code, Message: message, Cause: cause}
}

type Event struct {
	SchemaVersion       string          `json:"schema_version"`
	ID                  string          `json:"id"`
	RunID               string          `json:"run_id"`
	OperationID         string          `json:"operation_id"`
	Sequence            uint64          `json:"sequence"`
	Attempt             uint32          `json:"attempt"`
	Kind                string          `json:"kind"`
	From                *Status         `json:"from"`
	To                  Status          `json:"to"`
	OccurredAt          time.Time       `json:"occurred_at"`
	RecordedAt          time.Time       `json:"recorded_at"`
	Checkpoint          json.RawMessage `json:"checkpoint,omitempty"`
	PreviousEventSHA256 string          `json:"previous_event_sha256,omitempty"`
	EventSHA256         string          `json:"event_sha256,omitempty"`
}

type Run struct {
	ID              string          `json:"id"`
	Status          Status          `json:"status"`
	Sequence        uint64          `json:"sequence"`
	Attempt         uint32          `json:"attempt"`
	UpdatedAt       time.Time       `json:"updated_at"`
	LastEventID     string          `json:"last_event_id"`
	LastEventSHA256 string          `json:"last_event_sha256"`
	Checkpoint      json.RawMessage `json:"checkpoint,omitempty"`
	CheckpointAt    *time.Time      `json:"checkpoint_at,omitempty"`
}

type Journal struct {
	store              storage.Store
	maxCheckpointBytes int
	now                func() time.Time
}

type Option func(*Journal)

func WithMaxCheckpointBytes(limit int) Option {
	return func(journal *Journal) { journal.maxCheckpointBytes = limit }
}

func withClock(clock func() time.Time) Option {
	return func(journal *Journal) { journal.now = clock }
}

func NewJournal(store storage.Store, options ...Option) (*Journal, error) {
	journal := &Journal{
		store: store, maxCheckpointBytes: DefaultMaxCheckpointBytes,
		now: func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		option(journal)
	}
	if journal.store == nil || journal.maxCheckpointBytes < 1 || journal.now == nil {
		return nil, failure("FR_INVALID_CONFIGURATION", "store, clock, and a positive checkpoint limit are required", nil)
	}
	return journal, nil
}

type CreateInput struct {
	RunID       string
	OperationID string
	Checkpoint  []byte
}

func (journal *Journal) Create(ctx context.Context, input CreateInput) (Event, error) {
	if !validID.MatchString(input.OperationID) {
		return Event{}, failure("FR_INVALID_OPERATION", "operation id is invalid", nil)
	}
	runID := input.RunID
	var err error
	if runID == "" {
		runID, err = identity.New("run_")
		if err != nil {
			return Event{}, failure("FR_ID_GENERATION_FAILED", "run id could not be generated", err)
		}
	}
	if !validID.MatchString(runID) {
		return Event{}, failure("FR_INVALID_RUN", "run id is invalid", nil)
	}
	checkpoint, err := journal.normalizeCheckpoint(input.Checkpoint)
	if err != nil {
		return Event{}, err
	}
	now := journal.now().UTC()
	eventID, err := identity.New("evt_")
	if err != nil {
		return Event{}, failure("FR_ID_GENERATION_FAILED", "event id could not be generated", err)
	}
	event, err := (Event{
		SchemaVersion: EventSchema, ID: eventID, RunID: runID,
		OperationID: input.OperationID, Sequence: 0, Attempt: 0,
		Kind: EventRunCreated, To: StatusQueued,
		OccurredAt: now, RecordedAt: now, Checkpoint: checkpoint,
	}).Seal()
	if err != nil {
		return Event{}, err
	}
	return journal.commit(ctx, event)
}

type TransitionInput struct {
	RunID       string
	OperationID string
	To          Status
	Checkpoint  []byte
}

func (journal *Journal) Transition(ctx context.Context, input TransitionInput) (Event, error) {
	if !validID.MatchString(input.RunID) || !validID.MatchString(input.OperationID) {
		return Event{}, failure("FR_INVALID_OPERATION", "run and operation ids are required", nil)
	}
	events, err := journal.Events(ctx, input.RunID)
	if err != nil {
		return Event{}, err
	}
	checkpoint, err := journal.normalizeCheckpoint(input.Checkpoint)
	if err != nil {
		return Event{}, err
	}
	if existing, found := eventByOperation(events, input.OperationID); found {
		if existing.Kind == EventTransition && existing.To == input.To && bytes.Equal(existing.Checkpoint, checkpoint) {
			return existing, nil
		}
		return Event{}, failure("FR_IDEMPOTENCY_CONFLICT", fmt.Sprintf("operation id %q was reused", input.OperationID), nil)
	}
	last := events[len(events)-1]
	if err := ValidateTransition(last.To, input.To); err != nil {
		return Event{}, failure("FR_INVALID_TRANSITION", err.Error(), err)
	}
	attempt := last.Attempt
	if last.To == StatusQueued && input.To == StatusRunning {
		attempt++
	}
	now := journal.now().UTC()
	eventID, err := identity.New("evt_")
	if err != nil {
		return Event{}, failure("FR_ID_GENERATION_FAILED", "event id could not be generated", err)
	}
	from := last.To
	event, err := (Event{
		SchemaVersion: EventSchema, ID: eventID, RunID: input.RunID,
		OperationID: input.OperationID, Sequence: last.Sequence + 1,
		Attempt: attempt, Kind: EventTransition, From: &from, To: input.To,
		OccurredAt: now, RecordedAt: now, Checkpoint: checkpoint,
		PreviousEventSHA256: last.EventSHA256,
	}).Seal()
	if err != nil {
		return Event{}, err
	}
	return journal.commit(ctx, event)
}

type CheckpointInput struct {
	RunID       string
	OperationID string
	Checkpoint  []byte
}

func (journal *Journal) SaveCheckpoint(ctx context.Context, input CheckpointInput) (Event, error) {
	if !validID.MatchString(input.RunID) || !validID.MatchString(input.OperationID) {
		return Event{}, failure("FR_INVALID_OPERATION", "run and operation ids are required", nil)
	}
	checkpoint, err := journal.normalizeCheckpoint(input.Checkpoint)
	if err != nil {
		return Event{}, err
	}
	if checkpoint == nil {
		return Event{}, failure("FR_INVALID_CHECKPOINT", "checkpoint content is required", nil)
	}
	events, err := journal.Events(ctx, input.RunID)
	if err != nil {
		return Event{}, err
	}
	if existing, found := eventByOperation(events, input.OperationID); found {
		if existing.Kind == EventCheckpoint && bytes.Equal(existing.Checkpoint, checkpoint) {
			return existing, nil
		}
		return Event{}, failure("FR_IDEMPOTENCY_CONFLICT", fmt.Sprintf("operation id %q was reused", input.OperationID), nil)
	}
	last := events[len(events)-1]
	if last.To.Terminal() {
		return Event{}, failure("FR_INVALID_TRANSITION", "terminal runs cannot accept checkpoints", nil)
	}
	now := journal.now().UTC()
	eventID, err := identity.New("evt_")
	if err != nil {
		return Event{}, failure("FR_ID_GENERATION_FAILED", "event id could not be generated", err)
	}
	from := last.To
	event, err := (Event{
		SchemaVersion: EventSchema, ID: eventID, RunID: input.RunID,
		OperationID: input.OperationID, Sequence: last.Sequence + 1,
		Attempt: last.Attempt, Kind: EventCheckpoint, From: &from, To: last.To,
		OccurredAt: now, RecordedAt: now, Checkpoint: checkpoint,
		PreviousEventSHA256: last.EventSHA256,
	}).Seal()
	if err != nil {
		return Event{}, err
	}
	return journal.commit(ctx, event)
}

func (journal *Journal) Get(ctx context.Context, runID string) (Run, error) {
	events, err := journal.Events(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	return reduce(events), nil
}

func (journal *Journal) Events(ctx context.Context, runID string) ([]Event, error) {
	if !validID.MatchString(runID) {
		return nil, failure("FR_INVALID_RUN", "run id is invalid", nil)
	}
	keys, err := journal.store.List(ctx, runPrefix(runID)+"/events")
	if err != nil {
		return nil, failure("FR_EVENT_LIST_FAILED", "run events could not be listed", err)
	}
	if len(keys) == 0 {
		return nil, failure("FR_NOT_FOUND", "run was not found", storage.ErrNotFound)
	}
	events := make([]Event, 0, len(keys))
	for _, key := range keys {
		event, err := journal.readEventAt(ctx, key)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	sort.Slice(events, func(left, right int) bool { return events[left].Sequence < events[right].Sequence })
	if err := verifyChain(runID, events); err != nil {
		return nil, err
	}
	return events, nil
}

type VerificationIssue struct {
	Key   string `json:"key"`
	Error string `json:"error"`
}

type Verification struct {
	OK     bool                `json:"ok"`
	Store  string              `json:"store"`
	Runs   int                 `json:"runs"`
	Events int                 `json:"events"`
	Issues []VerificationIssue `json:"issues"`
}

// Verify reads every runtime event and validates its checksum, object key,
// sequence, status lineage, attempt counter, and hash-chain link.
func (journal *Journal) Verify(ctx context.Context) (Verification, error) {
	keys, err := journal.store.List(ctx, "runtime/v1/runs")
	if err != nil {
		return Verification{}, failure("FR_EVENT_LIST_FAILED", "runtime events could not be listed", err)
	}
	result := Verification{Store: journal.store.Description(), Issues: []VerificationIssue{}}
	groups := make(map[string][]string)
	for _, key := range keys {
		marker := strings.Index(key, "/events/")
		if marker < 0 || !strings.HasSuffix(key, ".json") {
			result.Issues = append(result.Issues, VerificationIssue{Key: key, Error: "unexpected object in runtime journal"})
			continue
		}
		groups[key[:marker]] = append(groups[key[:marker]], key)
	}
	groupNames := make([]string, 0, len(groups))
	for group := range groups {
		groupNames = append(groupNames, group)
	}
	sort.Strings(groupNames)
	result.Runs = len(groupNames)
	for _, group := range groupNames {
		events := make([]Event, 0, len(groups[group]))
		valid := true
		for _, key := range groups[group] {
			event, readErr := journal.readEventAt(ctx, key)
			if readErr != nil {
				result.Issues = append(result.Issues, VerificationIssue{Key: key, Error: readErr.Error()})
				valid = false
				continue
			}
			result.Events++
			events = append(events, event)
		}
		if !valid || len(events) == 0 {
			continue
		}
		sort.Slice(events, func(left, right int) bool { return events[left].Sequence < events[right].Sequence })
		if chainErr := verifyChain(events[0].RunID, events); chainErr != nil {
			result.Issues = append(result.Issues, VerificationIssue{Key: group, Error: chainErr.Error()})
		}
	}
	result.OK = len(result.Issues) == 0
	return result, nil
}

func (journal *Journal) commit(ctx context.Context, event Event) (Event, error) {
	encoded, err := canonicaljson.Marshal(event)
	if err != nil {
		return Event{}, failure("FR_INVALID_EVENT", "event cannot be encoded", err)
	}
	key := eventKey(event.RunID, event.Sequence)
	if _, err := journal.store.PutIfAbsent(ctx, key, encoded, storage.PutOptions{ContentType: "application/json"}); err != nil {
		if !errors.Is(err, storage.ErrConflict) {
			return Event{}, failure("FR_EVENT_WRITE_FAILED", "event could not be committed", err)
		}
		events, readErr := journal.Events(ctx, event.RunID)
		if readErr != nil {
			return Event{}, readErr
		}
		if existing, found := eventByOperation(events, event.OperationID); found {
			if sameOperation(existing, event) {
				return existing, nil
			}
			return Event{}, failure("FR_IDEMPOTENCY_CONFLICT", fmt.Sprintf("operation id %q was reused", event.OperationID), err)
		}
		return Event{}, failure("FR_CONCURRENT_TRANSITION", "another operation committed the next run event", err)
	}
	return event, nil
}

func (journal *Journal) readEventAt(ctx context.Context, key string) (Event, error) {
	data, err := journal.store.Get(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return Event{}, failure("FR_NOT_FOUND", "run event was not found", err)
	}
	if err != nil {
		return Event{}, failure("FR_EVENT_READ_FAILED", "run event could not be read", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var event Event
	if err := decoder.Decode(&event); err != nil {
		return Event{}, failure("FR_EVENT_CORRUPT", "run event is not valid JSON", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Event{}, failure("FR_EVENT_CORRUPT", "run event contains trailing JSON data", err)
	}
	if err := event.Verify(); err != nil {
		return Event{}, failure("FR_EVENT_CORRUPT", "run event failed validation", err)
	}
	if key != eventKey(event.RunID, event.Sequence) {
		return Event{}, failure("FR_EVENT_CORRUPT", "run event is stored under the wrong object key", nil)
	}
	return event, nil
}

func (journal *Journal) normalizeCheckpoint(input []byte) (json.RawMessage, error) {
	if input == nil {
		return nil, nil
	}
	checkpoint, err := canonicaljson.Normalize(input)
	if err != nil {
		return nil, failure("FR_INVALID_CHECKPOINT", "checkpoint is not valid JSON", err)
	}
	if len(checkpoint) > journal.maxCheckpointBytes {
		return nil, failure("FR_CHECKPOINT_TOO_LARGE", fmt.Sprintf("checkpoint is %d bytes; limit is %d", len(checkpoint), journal.maxCheckpointBytes), nil)
	}
	return json.RawMessage(checkpoint), nil
}

func (event Event) Validate() error {
	if event.SchemaVersion != EventSchema || !validID.MatchString(event.ID) || !validID.MatchString(event.RunID) || !validID.MatchString(event.OperationID) {
		return failure("FR_INVALID_EVENT", "event schema or identity is invalid", nil)
	}
	if event.OccurredAt.IsZero() || event.RecordedAt.IsZero() {
		return failure("FR_INVALID_EVENT", "event timestamps are required", nil)
	}
	if event.Checkpoint != nil {
		checkpoint, err := canonicaljson.Normalize(event.Checkpoint)
		if err != nil || !bytes.Equal(checkpoint, event.Checkpoint) {
			return failure("FR_INVALID_EVENT", "checkpoint is not canonical JSON", err)
		}
	}
	switch event.Kind {
	case EventRunCreated:
		if event.Sequence != 0 || event.Attempt != 0 || event.From != nil || event.To != StatusQueued || event.PreviousEventSHA256 != "" {
			return failure("FR_INVALID_EVENT", "run creation event is invalid", nil)
		}
	case EventTransition:
		if event.Sequence == 0 || event.From == nil || event.PreviousEventSHA256 == "" || !validDigest(event.PreviousEventSHA256) || ValidateTransition(*event.From, event.To) != nil {
			return failure("FR_INVALID_EVENT", "transition event is invalid", nil)
		}
	case EventCheckpoint:
		if event.Sequence == 0 || event.From == nil || *event.From != event.To || event.To.Terminal() || event.Checkpoint == nil || !validDigest(event.PreviousEventSHA256) {
			return failure("FR_INVALID_EVENT", "checkpoint event is invalid", nil)
		}
	default:
		return failure("FR_INVALID_EVENT", fmt.Sprintf("unsupported event kind %q", event.Kind), nil)
	}
	return nil
}

func (event Event) ComputeHash() (string, error) {
	event.EventSHA256 = ""
	data, err := canonicaljson.Marshal(event)
	if err != nil {
		return "", failure("FR_INVALID_EVENT", "event cannot be encoded", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func (event Event) Seal() (Event, error) {
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	digest, err := event.ComputeHash()
	if err != nil {
		return Event{}, err
	}
	event.EventSHA256 = digest
	return event, nil
}

func (event Event) Verify() error {
	if err := event.Validate(); err != nil {
		return err
	}
	expected, err := event.ComputeHash()
	if err != nil {
		return err
	}
	if event.EventSHA256 != expected {
		return failure("FR_EVENT_CORRUPT", fmt.Sprintf("event %q failed its checksum", event.ID), nil)
	}
	return nil
}

func verifyChain(runID string, events []Event) error {
	if len(events) == 0 {
		return failure("FR_EVENT_CORRUPT", "run has no creation event", nil)
	}
	operations := make(map[string]struct{}, len(events))
	var status Status
	var attempt uint32
	for index, event := range events {
		if event.RunID != runID || event.Sequence != uint64(index) {
			return failure("FR_EVENT_CORRUPT", "run event sequence is not contiguous", nil)
		}
		if _, exists := operations[event.OperationID]; exists {
			return failure("FR_EVENT_CORRUPT", fmt.Sprintf("operation id %q appears more than once", event.OperationID), nil)
		}
		operations[event.OperationID] = struct{}{}
		if index == 0 {
			status = event.To
			attempt = event.Attempt
			continue
		}
		previous := events[index-1]
		if event.PreviousEventSHA256 != previous.EventSHA256 || event.From == nil || *event.From != status {
			return failure("FR_EVENT_CORRUPT", "run event hash chain or status lineage is invalid", nil)
		}
		expectedAttempt := attempt
		if status == StatusQueued && event.To == StatusRunning {
			expectedAttempt++
		}
		if event.Attempt != expectedAttempt {
			return failure("FR_EVENT_CORRUPT", "run attempt lineage is invalid", nil)
		}
		attempt = event.Attempt
		status = event.To
	}
	return nil
}

func reduce(events []Event) Run {
	last := events[len(events)-1]
	run := Run{
		ID: last.RunID, Status: last.To, Sequence: last.Sequence,
		Attempt: last.Attempt, UpdatedAt: last.RecordedAt,
		LastEventID: last.ID, LastEventSHA256: last.EventSHA256,
	}
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Checkpoint != nil {
			run.Checkpoint = append(json.RawMessage(nil), events[index].Checkpoint...)
			value := events[index].RecordedAt
			run.CheckpointAt = &value
			break
		}
	}
	return run
}

func eventByOperation(events []Event, operationID string) (Event, bool) {
	for _, event := range events {
		if event.OperationID == operationID {
			return event, true
		}
	}
	return Event{}, false
}

func sameOperation(left, right Event) bool {
	return left.RunID == right.RunID &&
		left.OperationID == right.OperationID &&
		left.Kind == right.Kind &&
		sameStatus(left.From, right.From) &&
		left.To == right.To &&
		bytes.Equal(left.Checkpoint, right.Checkpoint)
}

func sameStatus(left, right *Status) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func runPrefix(runID string) string {
	digest := sha256.Sum256([]byte(runID))
	value := hex.EncodeToString(digest[:])
	return fmt.Sprintf("runtime/v1/runs/%s/%s", value[:2], value)
}

func eventKey(runID string, sequence uint64) string {
	return fmt.Sprintf("%s/events/%020d.json", runPrefix(runID), sequence)
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
