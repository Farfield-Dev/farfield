package runtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Farfield-Dev/farfield/storage"
)

func TestJournalLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	journal := newTestJournal(t, nil)

	created, err := journal.Create(ctx, CreateInput{RunID: "run_lifecycle", OperationID: "create", Checkpoint: []byte(`{"step":0}`)})
	if err != nil {
		t.Fatal(err)
	}
	if created.Sequence != 0 || created.To != StatusQueued {
		t.Fatalf("created event = %#v", created)
	}
	running, err := journal.Transition(ctx, TransitionInput{RunID: created.RunID, OperationID: "start", To: StatusRunning})
	if err != nil {
		t.Fatal(err)
	}
	if running.Attempt != 1 || running.PreviousEventSHA256 != created.EventSHA256 {
		t.Fatalf("running event = %#v", running)
	}
	checkpoint, err := journal.SaveCheckpoint(ctx, CheckpointInput{RunID: created.RunID, OperationID: "save", Checkpoint: []byte(`{ "cursor": 42 }`)})
	if err != nil {
		t.Fatal(err)
	}
	if string(checkpoint.Checkpoint) != `{"cursor":42}` {
		t.Fatalf("checkpoint = %s", checkpoint.Checkpoint)
	}
	completed, err := journal.Transition(ctx, TransitionInput{RunID: created.RunID, OperationID: "finish", To: StatusCompleted})
	if err != nil {
		t.Fatal(err)
	}

	run, err := journal.Get(ctx, created.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != StatusCompleted || run.Sequence != 3 || run.Attempt != 1 || run.LastEventID != completed.ID || string(run.Checkpoint) != `{"cursor":42}` {
		t.Fatalf("run = %#v", run)
	}
	events, err := journal.Events(ctx, created.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4", len(events))
	}
	verification, err := journal.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.OK || verification.Runs != 1 || verification.Events != 4 {
		t.Fatalf("verification = %#v", verification)
	}
}

func TestJournalRejectsInvalidTransitionWithoutWriting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	journal := newTestJournal(t, nil)
	if _, err := journal.Create(ctx, CreateInput{RunID: "run_invalid", OperationID: "create"}); err != nil {
		t.Fatal(err)
	}
	_, err := journal.Transition(ctx, TransitionInput{RunID: "run_invalid", OperationID: "skip", To: StatusCompleted})
	assertCode(t, err, "FR_INVALID_TRANSITION")
	events, readErr := journal.Events(ctx, "run_invalid")
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
}

func TestJournalIdempotentOperationsAndConflicts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	journal := newTestJournal(t, nil)
	first, err := journal.Create(ctx, CreateInput{RunID: "run_retry", OperationID: "create", Checkpoint: []byte(`{"a":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := journal.Create(ctx, CreateInput{RunID: "run_retry", OperationID: "create", Checkpoint: []byte(`{ "a": 1 }`)})
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID != first.ID {
		t.Fatalf("retry event id = %q, want %q", retry.ID, first.ID)
	}
	_, err = journal.Create(ctx, CreateInput{RunID: "run_retry", OperationID: "create", Checkpoint: []byte(`{"a":2}`)})
	assertCode(t, err, "FR_IDEMPOTENCY_CONFLICT")

	started, err := journal.Transition(ctx, TransitionInput{RunID: "run_retry", OperationID: "start", To: StatusRunning})
	if err != nil {
		t.Fatal(err)
	}
	startedRetry, err := journal.Transition(ctx, TransitionInput{RunID: "run_retry", OperationID: "start", To: StatusRunning})
	if err != nil {
		t.Fatal(err)
	}
	if startedRetry.ID != started.ID {
		t.Fatalf("transition retry id = %q, want %q", startedRetry.ID, started.ID)
	}
	_, err = journal.SaveCheckpoint(ctx, CheckpointInput{RunID: "run_retry", OperationID: "start", Checkpoint: []byte(`{"a":1}`)})
	assertCode(t, err, "FR_IDEMPOTENCY_CONFLICT")
}

func TestJournalSerializesConcurrentTransitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	journal := newTestJournal(t, nil)
	if _, err := journal.Create(ctx, CreateInput{RunID: "run_race", OperationID: "create"}); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errorsByOperation := make(chan error, 2)
	for _, operationID := range []string{"worker_a", "worker_b"} {
		operationID := operationID
		go func() {
			<-start
			_, err := journal.Transition(ctx, TransitionInput{RunID: "run_race", OperationID: operationID, To: StatusRunning})
			errorsByOperation <- err
		}()
	}
	close(start)
	var successes, conflicts int
	for range 2 {
		err := <-errorsByOperation
		if err == nil {
			successes++
		} else if hasCode(err, "FR_CONCURRENT_TRANSITION") {
			conflicts++
		} else {
			t.Fatalf("unexpected transition error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes = %d, conflicts = %d", successes, conflicts)
	}
	events, err := journal.Events(ctx, "run_race")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].To != StatusRunning {
		t.Fatalf("events = %#v", events)
	}
}

func TestJournalCoalescesConcurrentSameOperation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	journal := newTestJournal(t, nil)
	if _, err := journal.Create(ctx, CreateInput{RunID: "run_same", OperationID: "create"}); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan Event, 8)
	errs := make(chan error, 8)
	for range 8 {
		go func() {
			<-start
			event, err := journal.Transition(ctx, TransitionInput{RunID: "run_same", OperationID: "start", To: StatusRunning})
			results <- event
			errs <- err
		}()
	}
	close(start)
	var eventID string
	for range 8 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		event := <-results
		if eventID == "" {
			eventID = event.ID
		} else if event.ID != eventID {
			t.Fatalf("event id = %q, want %q", event.ID, eventID)
		}
	}
	journalEvents, err := journal.Events(ctx, "run_same")
	if err != nil {
		t.Fatal(err)
	}
	if len(journalEvents) != 2 {
		t.Fatalf("events = %d, want 2", len(journalEvents))
	}
}

func TestJournalRecoversFromAmbiguousCommitOnRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &ambiguousStore{Store: base, failNext: true}
	journal := newTestJournal(t, store)
	input := CreateInput{RunID: "run_ambiguous", OperationID: "create", Checkpoint: []byte(`{"safe":true}`)}
	if _, err := journal.Create(ctx, input); err == nil {
		t.Fatal("ambiguous write unexpectedly succeeded")
	} else {
		assertCode(t, err, "FR_EVENT_WRITE_FAILED")
	}
	recovered, err := journal.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Sequence != 0 || recovered.OperationID != "create" {
		t.Fatalf("recovered = %#v", recovered)
	}
	events, err := journal.Events(ctx, input.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
}

func TestJournalRecoversAmbiguousTransitionOnRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &ambiguousStore{Store: base}
	journal := newTestJournal(t, store)
	if _, err := journal.Create(ctx, CreateInput{RunID: "run_transition_ambiguous", OperationID: "create"}); err != nil {
		t.Fatal(err)
	}
	store.arm()
	input := TransitionInput{RunID: "run_transition_ambiguous", OperationID: "start", To: StatusRunning, Checkpoint: []byte(`{"step":1}`)}
	if _, err := journal.Transition(ctx, input); err == nil {
		t.Fatal("ambiguous transition unexpectedly succeeded")
	} else {
		assertCode(t, err, "FR_EVENT_WRITE_FAILED")
	}
	recovered, err := journal.Transition(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Sequence != 1 || recovered.OperationID != "start" || recovered.To != StatusRunning {
		t.Fatalf("recovered = %#v", recovered)
	}
	events, err := journal.Events(ctx, input.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
}

func TestJournalTracksAttemptsAcrossResume(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	journal := newTestJournal(t, nil)
	steps := []TransitionInput{
		{RunID: "run_attempts", OperationID: "start_1", To: StatusRunning},
		{RunID: "run_attempts", OperationID: "wait", To: StatusWaiting},
		{RunID: "run_attempts", OperationID: "resume", To: StatusQueued},
		{RunID: "run_attempts", OperationID: "start_2", To: StatusRunning},
	}
	if _, err := journal.Create(ctx, CreateInput{RunID: "run_attempts", OperationID: "create"}); err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if _, err := journal.Transition(ctx, step); err != nil {
			t.Fatal(err)
		}
	}
	run, err := journal.Get(ctx, "run_attempts")
	if err != nil {
		t.Fatal(err)
	}
	if run.Attempt != 2 || run.Status != StatusRunning {
		t.Fatalf("run = %#v", run)
	}
}

func TestJournalVerificationReportsCorruption(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	journal := newTestJournal(t, base)
	event, err := journal.Create(ctx, CreateInput{RunID: "run_corrupt", OperationID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	corrupt := &corruptingStore{Store: base, key: eventKey(event.RunID, event.Sequence)}
	verifier := newTestJournal(t, corrupt)
	verification, err := verifier.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if verification.OK || len(verification.Issues) != 1 || !strings.Contains(verification.Issues[0].Error, "FR_EVENT_CORRUPT") {
		t.Fatalf("verification = %#v", verification)
	}
}

func newTestJournal(t *testing.T, store storage.Store) *Journal {
	t.Helper()
	if store == nil {
		var err error
		store, err = storage.OpenLocal(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
	}
	journal, err := NewJournal(store)
	if err != nil {
		t.Fatal(err)
	}
	return journal
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	if !hasCode(err, code) {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func hasCode(err error, code string) bool {
	var runtimeError *Error
	return errors.As(err, &runtimeError) && runtimeError.Code == code
}

type ambiguousStore struct {
	storage.Store
	mu       sync.Mutex
	failNext bool
}

func (store *ambiguousStore) arm() {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.failNext = true
}

func (store *ambiguousStore) PutIfAbsent(ctx context.Context, key string, data []byte, options storage.PutOptions) (bool, error) {
	created, err := store.Store.PutIfAbsent(ctx, key, data, options)
	if err != nil || !created {
		return created, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failNext {
		store.failNext = false
		return true, errors.New("connection closed after commit")
	}
	return true, nil
}

type corruptingStore struct {
	storage.Store
	key string
}

func (store *corruptingStore) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := store.Store.Get(ctx, key)
	if err != nil || key != store.key {
		return data, err
	}
	return []byte(strings.Replace(string(data), `"queued"`, `"failed"`, 1)), nil
}
