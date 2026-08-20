package history

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/Farfield-Dev/farfield/internal/canonicaljson"
	"github.com/Farfield-Dev/farfield/storage"
)

const (
	conversationDeltaSchema       = "farfield.projection.conversations.delta.v1"
	conversationSnapshotSchema    = "farfield.projection.conversations.snapshot.v1"
	conversationDeltaPrefix       = "projections/v1/conversations/deltas"
	conversationSnapshotPrefix    = "projections/v1/conversations/snapshots"
	conversationRefreshInterval   = time.Second
	conversationSnapshotThreshold = 256
	conversationReadConcurrency   = 16
)

// conversationProjection is a rebuildable materialized view. Its objects are
// never authoritative: records and segments remain the source of truth.
type conversationProjection struct {
	store storage.Store
	now   func() time.Time

	mu                  sync.Mutex
	loaded              bool
	values              map[string]*conversationAggregate
	applied             map[string]struct{}
	lastRefresh         time.Time
	deltasSinceSnapshot int
}

type conversationAggregate struct {
	conversation Conversation
	agents       map[string]struct{}
	kinds        map[string]struct{}
}

type conversationDelta struct {
	SchemaVersion  string    `json:"schema_version"`
	SourceKey      string    `json:"source_key"`
	SourceSHA256   string    `json:"source_sha256"`
	ConversationID string    `json:"conversation_id"`
	RecordCount    int       `json:"record_count"`
	FirstSeenAt    time.Time `json:"first_seen_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
	Agents         []string  `json:"agents"`
	Kinds          []string  `json:"kinds"`
	DeltaSHA256    string    `json:"delta_sha256,omitempty"`
}

type conversationSnapshot struct {
	SchemaVersion    string         `json:"schema_version"`
	CreatedAt        time.Time      `json:"created_at"`
	Conversations    []Conversation `json:"conversations"`
	AppliedDeltaKeys []string       `json:"applied_delta_keys"`
	SnapshotSHA256   string         `json:"snapshot_sha256,omitempty"`
}

// ProjectionRebuild summarizes an explicit reconstruction from authoritative
// History objects.
type ProjectionRebuild struct {
	Projection        string `json:"projection"`
	ConversationCount int    `json:"conversation_count"`
	SourceCount       int    `json:"source_count"`
}

func newConversationProjection(store storage.Store, now func() time.Time) *conversationProjection {
	return &conversationProjection{store: store, now: now}
}

func conversationDeltaKey(sourceKey string) string {
	digest := sha256.Sum256([]byte(sourceKey))
	value := hex.EncodeToString(digest[:])
	return fmt.Sprintf("%s/%s/%s.json", conversationDeltaPrefix, value[:2], value[2:])
}

func buildConversationDelta(sourceKey, sourceSHA string, records []Record) (conversationDelta, error) {
	if len(records) == 0 {
		return conversationDelta{}, fmt.Errorf("source contains no records")
	}
	conversationID := records[0].ConversationID
	agents := map[string]struct{}{}
	kinds := map[string]struct{}{}
	first := records[0].OccurredAt
	last := first
	for _, record := range records {
		if record.ConversationID != conversationID {
			return conversationDelta{}, fmt.Errorf("source contains multiple conversations")
		}
		if record.OccurredAt.Before(first) {
			first = record.OccurredAt
		}
		if record.OccurredAt.After(last) {
			last = record.OccurredAt
		}
		if record.Agent != nil && *record.Agent != "" {
			agents[*record.Agent] = struct{}{}
		}
		kinds[record.Kind] = struct{}{}
	}
	delta := conversationDelta{
		SchemaVersion: conversationDeltaSchema, SourceKey: sourceKey, SourceSHA256: sourceSHA,
		ConversationID: conversationID, RecordCount: len(records), FirstSeenAt: first,
		LastSeenAt: last, Agents: sortedKeys(agents), Kinds: sortedKeys(kinds),
	}
	digest, err := hashConversationDelta(delta)
	if err != nil {
		return conversationDelta{}, err
	}
	delta.DeltaSHA256 = digest
	return delta, nil
}

func hashConversationDelta(delta conversationDelta) (string, error) {
	delta.DeltaSHA256 = ""
	encoded, err := canonicaljson.Marshal(delta)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (projection *conversationProjection) append(ctx context.Context, delta conversationDelta) error {
	encoded, err := canonicaljson.Marshal(delta)
	if err != nil {
		return err
	}
	key := conversationDeltaKey(delta.SourceKey)
	if _, err := projection.store.PutIfAbsent(ctx, key, encoded, storage.PutOptions{ContentType: "application/json"}); err != nil {
		return err
	}
	projection.mu.Lock()
	defer projection.mu.Unlock()
	if projection.loaded {
		projection.apply(key, delta)
	}
	return nil
}

func (service *Service) projectSource(ctx context.Context, sourceKey, sourceSHA string, records []Record) error {
	delta, err := buildConversationDelta(sourceKey, sourceSHA, records)
	if err != nil {
		return failure("FH_PROJECTION_WRITE_FAILED", "conversation projection could not be built", err)
	}
	if err := service.projection.append(ctx, delta); err != nil {
		return failure("FH_PROJECTION_WRITE_FAILED", "conversation projection could not be committed; retry with the same id to repair it", err)
	}
	return nil
}

func (projection *conversationProjection) conversations(ctx context.Context, limit int) ([]Conversation, error) {
	projection.mu.Lock()
	defer projection.mu.Unlock()

	if !projection.loaded {
		if err := projection.load(ctx); err != nil {
			return nil, failure("FH_PROJECTION_READ_FAILED", "conversation projection could not be loaded", err)
		}
	} else if projection.now().Sub(projection.lastRefresh) >= conversationRefreshInterval {
		if err := projection.refresh(ctx); err != nil {
			return nil, failure("FH_PROJECTION_READ_FAILED", "conversation projection could not be refreshed; run `farfield history projections rebuild`", err)
		}
	}
	if projection.deltasSinceSnapshot >= conversationSnapshotThreshold {
		if err := projection.writeSnapshot(ctx); err != nil {
			return nil, failure("FH_PROJECTION_WRITE_FAILED", "conversation projection snapshot could not be committed", err)
		}
	}
	return projection.result(limit), nil
}

func (projection *conversationProjection) load(ctx context.Context) error {
	keys, err := projection.store.List(ctx, conversationSnapshotPrefix)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		projection.values = make(map[string]*conversationAggregate)
		projection.applied = make(map[string]struct{})
		projection.loaded = true
		projection.lastRefresh = time.Time{}
		if err := projection.refresh(ctx); err != nil {
			return err
		}
		return projection.writeSnapshot(ctx)
	}
	for index := len(keys) - 1; index >= 0; index-- {
		snapshot, readErr := projection.readSnapshot(ctx, keys[index])
		if readErr != nil {
			continue
		}
		projection.install(snapshot)
		return projection.refresh(ctx)
	}
	return fmt.Errorf("no valid conversation projection snapshot; run `farfield history projections rebuild`")
}

// RebuildConversationProjection reconstructs the disposable conversation view
// by scanning authoritative records and segments and publishing a new snapshot.
func (service *Service) RebuildConversationProjection(ctx context.Context) (ProjectionRebuild, error) {
	service.projection.mu.Lock()
	defer service.projection.mu.Unlock()
	if err := service.projection.rebuild(ctx, service); err != nil {
		return ProjectionRebuild{}, failure("FH_PROJECTION_REBUILD_FAILED", "conversation projection could not be rebuilt", err)
	}
	return ProjectionRebuild{
		Projection: "conversations", ConversationCount: len(service.projection.values),
		SourceCount: len(service.projection.applied),
	}, nil
}

func (projection *conversationProjection) rebuild(ctx context.Context, service *Service) error {
	records, segments, err := service.listRecordsWithSegments(ctx, "segments/v1/shards")
	if err != nil {
		return err
	}
	projection.values = aggregatesFromConversations(aggregateConversations(records, -1))
	projection.applied = make(map[string]struct{})
	for _, record := range records {
		if record.SchemaVersion == RecordSchema {
			projection.applied[conversationDeltaKey(recordKey(record.ID))] = struct{}{}
		}
	}
	for key := range segments {
		projection.applied[conversationDeltaKey(key)] = struct{}{}
	}
	projection.loaded = true
	projection.lastRefresh = time.Time{}
	projection.deltasSinceSnapshot = 0
	if err := projection.refresh(ctx); err != nil {
		return err
	}
	return projection.writeSnapshot(ctx)
}

func (projection *conversationProjection) refresh(ctx context.Context) error {
	keys, err := projection.store.List(ctx, conversationDeltaPrefix)
	if err != nil {
		return err
	}
	unseen := make([]string, 0)
	for _, key := range keys {
		if _, exists := projection.applied[key]; exists {
			continue
		}
		unseen = append(unseen, key)
	}
	deltas, err := projection.readDeltas(ctx, unseen)
	if err != nil {
		return err
	}
	for index, delta := range deltas {
		projection.apply(unseen[index], delta)
	}
	projection.lastRefresh = projection.now()
	return nil
}

func (projection *conversationProjection) readDeltas(ctx context.Context, keys []string) ([]conversationDelta, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	deltas := make([]conversationDelta, len(keys))
	jobs := make(chan int)
	workers := min(conversationReadConcurrency, len(keys))
	var wait sync.WaitGroup
	var firstError error
	var errorOnce sync.Once
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range jobs {
				delta, err := projection.readDelta(ctx, keys[index])
				if err != nil {
					errorOnce.Do(func() { firstError = err })
					continue
				}
				deltas[index] = delta
			}
		}()
	}
	for index := range keys {
		jobs <- index
	}
	close(jobs)
	wait.Wait()
	return deltas, firstError
}

func (projection *conversationProjection) apply(key string, delta conversationDelta) {
	if _, exists := projection.applied[key]; exists {
		return
	}
	value := projection.values[delta.ConversationID]
	if value == nil {
		value = &conversationAggregate{
			conversation: Conversation{ID: delta.ConversationID, FirstSeenAt: delta.FirstSeenAt, LastSeenAt: delta.LastSeenAt},
			agents:       map[string]struct{}{}, kinds: map[string]struct{}{},
		}
		projection.values[delta.ConversationID] = value
	}
	value.conversation.RecordCount += delta.RecordCount
	if delta.FirstSeenAt.Before(value.conversation.FirstSeenAt) {
		value.conversation.FirstSeenAt = delta.FirstSeenAt
	}
	if delta.LastSeenAt.After(value.conversation.LastSeenAt) {
		value.conversation.LastSeenAt = delta.LastSeenAt
	}
	for _, agent := range delta.Agents {
		value.agents[agent] = struct{}{}
	}
	for _, kind := range delta.Kinds {
		value.kinds[kind] = struct{}{}
	}
	projection.applied[key] = struct{}{}
	projection.deltasSinceSnapshot++
}

func (projection *conversationProjection) result(limit int) []Conversation {
	result := make([]Conversation, 0, len(projection.values))
	for _, value := range projection.values {
		conversation := value.conversation
		conversation.Agents = sortedKeys(value.agents)
		conversation.Kinds = sortedKeys(value.kinds)
		result = append(result, conversation)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].LastSeenAt.Equal(result[right].LastSeenAt) {
			return result[left].ID < result[right].ID
		}
		return result[left].LastSeenAt.After(result[right].LastSeenAt)
	})
	if limit == 0 {
		limit = 100
	}
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result
}

func aggregatesFromConversations(conversations []Conversation) map[string]*conversationAggregate {
	values := make(map[string]*conversationAggregate, len(conversations))
	for _, conversation := range conversations {
		agents := make(map[string]struct{}, len(conversation.Agents))
		for _, agent := range conversation.Agents {
			agents[agent] = struct{}{}
		}
		kinds := make(map[string]struct{}, len(conversation.Kinds))
		for _, kind := range conversation.Kinds {
			kinds[kind] = struct{}{}
		}
		values[conversation.ID] = &conversationAggregate{conversation: conversation, agents: agents, kinds: kinds}
	}
	return values
}

func (projection *conversationProjection) writeSnapshot(ctx context.Context) error {
	snapshot := conversationSnapshot{
		SchemaVersion:    conversationSnapshotSchema,
		CreatedAt:        projection.now().UTC(),
		Conversations:    projection.result(-1),
		AppliedDeltaKeys: make([]string, 0, len(projection.applied)),
	}
	for key := range projection.applied {
		snapshot.AppliedDeltaKeys = append(snapshot.AppliedDeltaKeys, key)
	}
	sort.Strings(snapshot.AppliedDeltaKeys)
	digest, err := hashConversationSnapshot(snapshot)
	if err != nil {
		return err
	}
	snapshot.SnapshotSHA256 = digest
	encoded, err := canonicaljson.Marshal(snapshot)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%s/%020d-%020d-%s.json", conversationSnapshotPrefix, len(snapshot.AppliedDeltaKeys), snapshot.CreatedAt.UnixNano(), digest)
	if _, err := projection.store.PutIfAbsent(ctx, key, encoded, storage.PutOptions{ContentType: "application/json"}); err != nil {
		return err
	}
	projection.deltasSinceSnapshot = 0
	return nil
}

func hashConversationSnapshot(snapshot conversationSnapshot) (string, error) {
	snapshot.SnapshotSHA256 = ""
	encoded, err := canonicaljson.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (projection *conversationProjection) install(snapshot conversationSnapshot) {
	projection.values = aggregatesFromConversations(snapshot.Conversations)
	projection.applied = make(map[string]struct{}, len(snapshot.AppliedDeltaKeys))
	for _, key := range snapshot.AppliedDeltaKeys {
		projection.applied[key] = struct{}{}
	}
	projection.loaded = true
	projection.lastRefresh = time.Time{}
	projection.deltasSinceSnapshot = 0
}

func (projection *conversationProjection) readDelta(ctx context.Context, key string) (conversationDelta, error) {
	var delta conversationDelta
	if err := readProjectionJSON(ctx, projection.store, key, &delta); err != nil {
		return delta, err
	}
	if delta.SchemaVersion != conversationDeltaSchema || key != conversationDeltaKey(delta.SourceKey) || delta.RecordCount < 1 || delta.ConversationID == "" || delta.FirstSeenAt.IsZero() || delta.LastSeenAt.Before(delta.FirstSeenAt) {
		return delta, fmt.Errorf("invalid conversation delta %s", key)
	}
	expected, err := hashConversationDelta(delta)
	if err != nil || expected != delta.DeltaSHA256 {
		return delta, fmt.Errorf("conversation delta %s failed its checksum", key)
	}
	return delta, nil
}

func (projection *conversationProjection) readSnapshot(ctx context.Context, key string) (conversationSnapshot, error) {
	var snapshot conversationSnapshot
	if err := readProjectionJSON(ctx, projection.store, key, &snapshot); err != nil {
		return snapshot, err
	}
	if snapshot.SchemaVersion != conversationSnapshotSchema || snapshot.CreatedAt.IsZero() || !sort.StringsAreSorted(snapshot.AppliedDeltaKeys) {
		return snapshot, fmt.Errorf("invalid conversation snapshot %s", key)
	}
	expected, err := hashConversationSnapshot(snapshot)
	if err != nil || expected != snapshot.SnapshotSHA256 {
		return snapshot, fmt.Errorf("conversation snapshot %s failed its checksum", key)
	}
	return snapshot, nil
}

func readProjectionJSON(ctx context.Context, store storage.Store, key string, target any) error {
	data, err := store.Get(ctx, key)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("object contains trailing JSON data")
	}
	return nil
}
