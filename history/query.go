package history

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

type Query struct {
	ConversationID string
	TraceID        string
	Kind           string
	Agent          string
	Tool           string
	Status         string
	Tags           map[string]string
	Since          *time.Time
	Limit          int
}

func (service *Service) Query(ctx context.Context, query Query) ([]Record, error) {
	segmentPrefix := "segments/v1/shards"
	if query.ConversationID != "" {
		segmentPrefix += "/" + segmentShard(query.ConversationID)
	}
	records, err := service.listRecords(ctx, segmentPrefix)
	if err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit == 0 {
		limit = 100
	}
	capacity := len(records)
	if limit > 0 {
		capacity = min(capacity, limit)
	}
	result := make([]Record, 0, capacity)
	for _, record := range records {
		if !matches(record, query) {
			continue
		}
		result = append(result, record)
		if limit > 0 && len(result) == limit {
			break
		}
	}
	return result, nil
}

type Entry struct {
	Record  Record          `json:"record"`
	Content json.RawMessage `json:"content"`
}

func (service *Service) Timeline(ctx context.Context, conversationID string) ([]Entry, error) {
	records, segments, err := service.listRecordsWithSegments(ctx, "segments/v1/shards/"+segmentShard(conversationID))
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(records))
	for _, record := range records {
		if record.ConversationID != conversationID {
			continue
		}
		content, err := service.readContent(ctx, record, segments)
		if err != nil {
			return nil, err
		}
		entries = append(entries, Entry{Record: record, Content: json.RawMessage(content)})
	}
	if len(entries) == 0 {
		return nil, failure("FH_NOT_FOUND", "conversation was not found", nil)
	}
	return entries, nil
}

type Conversation struct {
	ID          string    `json:"id"`
	RecordCount int       `json:"record_count"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	Agents      []string  `json:"agents"`
	Kinds       []string  `json:"kinds"`
}

func (service *Service) Conversations(ctx context.Context, limit int) ([]Conversation, error) {
	return service.projection.conversations(ctx, service, limit)
}

func aggregateConversations(records []Record, limit int) []Conversation {
	type aggregate struct {
		conversation Conversation
		agents       map[string]struct{}
		kinds        map[string]struct{}
	}
	values := make(map[string]*aggregate)
	for _, record := range records {
		value := values[record.ConversationID]
		if value == nil {
			value = &aggregate{
				conversation: Conversation{ID: record.ConversationID, FirstSeenAt: record.OccurredAt, LastSeenAt: record.OccurredAt},
				agents:       map[string]struct{}{}, kinds: map[string]struct{}{},
			}
			values[record.ConversationID] = value
		}
		value.conversation.RecordCount++
		if record.OccurredAt.Before(value.conversation.FirstSeenAt) {
			value.conversation.FirstSeenAt = record.OccurredAt
		}
		if record.OccurredAt.After(value.conversation.LastSeenAt) {
			value.conversation.LastSeenAt = record.OccurredAt
		}
		if record.Agent != nil && *record.Agent != "" {
			value.agents[*record.Agent] = struct{}{}
		}
		value.kinds[record.Kind] = struct{}{}
	}
	result := make([]Conversation, 0, len(values))
	for _, value := range values {
		value.conversation.Agents = sortedKeys(value.agents)
		value.conversation.Kinds = sortedKeys(value.kinds)
		result = append(result, value.conversation)
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

func matches(record Record, query Query) bool {
	if query.ConversationID != "" && record.ConversationID != query.ConversationID {
		return false
	}
	if !matchesOptional(query.TraceID, record.TraceID) ||
		!matchesOptional(query.Agent, record.Agent) ||
		!matchesOptional(query.Tool, record.Tool) ||
		!matchesOptional(query.Status, record.Status) {
		return false
	}
	if query.Kind != "" && record.Kind != query.Kind {
		return false
	}
	if query.Since != nil && record.OccurredAt.Before(*query.Since) {
		return false
	}
	for key, value := range query.Tags {
		if record.Tags[key] != value {
			return false
		}
	}
	return true
}

func matchesOptional(expected string, actual *string) bool {
	return expected == "" || (actual != nil && *actual == expected)
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
