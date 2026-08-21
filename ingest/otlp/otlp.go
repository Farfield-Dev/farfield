// Package otlp converts OpenTelemetry trace exports into immutable Farfield
// History records. The original OTLP evidence is retained in record content;
// normalized fields only make common agent operations easy to query.
package otlp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Farfield-Dev/farfield/history"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

const ContentSchema = "farfield.otel.span.v1"

// Result describes an OTLP export after it has been committed to History.
type Result struct {
	Accepted int
	Rejected int
	Errors   []string
}

// Ingestor durably commits completed OTLP spans to Farfield History.
type Ingestor struct {
	history *history.Service
}

func New(service *history.Service) (*Ingestor, error) {
	if service == nil {
		return nil, errors.New("OTLP ingestor requires a History service")
	}
	return &Ingestor{history: service}, nil
}

// Export normalizes and durably commits every valid span in request. Spans are
// grouped into conversation-local segments so one object commit can make a
// normal exporter batch durable. Stable OTLP trace/span IDs make exact retries
// idempotent.
func (ingestor *Ingestor) Export(ctx context.Context, resourceSpans []*tracev1.ResourceSpans) (Result, error) {
	groups := map[string][]history.AppendInput{}
	result := Result{}
	for _, resourceGroup := range resourceSpans {
		resourceAttrs := attributes(resourceGroup.GetResource().GetAttributes())
		for _, scopeGroup := range resourceGroup.GetScopeSpans() {
			scope := scopeContent(scopeGroup.GetScope(), scopeGroup.GetSchemaUrl())
			for _, span := range scopeGroup.GetSpans() {
				input, err := normalizeSpan(resourceGroup.GetResource(), resourceGroup.GetSchemaUrl(), resourceAttrs, scope, span)
				if err != nil {
					result.Rejected++
					result.Errors = append(result.Errors, err.Error())
					continue
				}
				groups[input.ConversationID] = append(groups[input.ConversationID], input)
			}
		}
	}

	conversationIDs := make([]string, 0, len(groups))
	for conversationID := range groups {
		conversationIDs = append(conversationIDs, conversationID)
	}
	sort.Strings(conversationIDs)
	for _, conversationID := range conversationIDs {
		inputs := groups[conversationID]
		sort.Slice(inputs, func(left, right int) bool { return inputs[left].RecordID < inputs[right].RecordID })
		for start := 0; start < len(inputs); start += history.DefaultMaxSegmentRecords {
			end := min(start+history.DefaultMaxSegmentRecords, len(inputs))
			chunk := inputs[start:end]
			segmentID, err := stableSegmentID(chunk)
			if err != nil {
				return result, fmt.Errorf("encode OTLP segment identity: %w", err)
			}
			if _, err := ingestor.history.AppendBatch(ctx, history.AppendBatchInput{
				SegmentID: segmentID,
				Records:   chunk,
			}); err != nil {
				return result, fmt.Errorf("commit OTLP conversation %q: %w", conversationID, err)
			}
			result.Accepted += len(chunk)
		}
	}
	return result, nil
}

type spanContent struct {
	Schema            string                 `json:"schema"`
	Name              string                 `json:"name"`
	Kind              string                 `json:"span_kind"`
	StartTime         *time.Time             `json:"start_time,omitempty"`
	EndTime           *time.Time             `json:"end_time,omitempty"`
	DurationMS        *float64               `json:"duration_ms,omitempty"`
	TraceState        string                 `json:"trace_state,omitempty"`
	Flags             uint32                 `json:"flags,omitempty"`
	Attributes        map[string]any         `json:"attributes,omitempty"`
	Resource          resourceContent        `json:"resource"`
	Scope             instrumentationContent `json:"scope"`
	Events            []eventContent         `json:"events,omitempty"`
	Links             []linkContent          `json:"links,omitempty"`
	Status            statusContent          `json:"status"`
	DroppedAttributes uint32                 `json:"dropped_attributes,omitempty"`
	DroppedEvents     uint32                 `json:"dropped_events,omitempty"`
	DroppedLinks      uint32                 `json:"dropped_links,omitempty"`
	Input             any                    `json:"input,omitempty"`
	Output            any                    `json:"output,omitempty"`
	Model             string                 `json:"model,omitempty"`
	Provider          string                 `json:"provider,omitempty"`
	Usage             map[string]any         `json:"usage,omitempty"`
}

type resourceContent struct {
	SchemaURL         string         `json:"schema_url,omitempty"`
	Attributes        map[string]any `json:"attributes,omitempty"`
	DroppedAttributes uint32         `json:"dropped_attributes,omitempty"`
}

type instrumentationContent struct {
	Name      string         `json:"name,omitempty"`
	Version   string         `json:"version,omitempty"`
	SchemaURL string         `json:"schema_url,omitempty"`
	Attrs     map[string]any `json:"attributes,omitempty"`
}

type eventContent struct {
	Name              string         `json:"name"`
	Time              *time.Time     `json:"time,omitempty"`
	Attributes        map[string]any `json:"attributes,omitempty"`
	DroppedAttributes uint32         `json:"dropped_attributes,omitempty"`
}

type linkContent struct {
	TraceID           string         `json:"trace_id"`
	SpanID            string         `json:"span_id"`
	TraceState        string         `json:"trace_state,omitempty"`
	Attributes        map[string]any `json:"attributes,omitempty"`
	DroppedAttributes uint32         `json:"dropped_attributes,omitempty"`
	Flags             uint32         `json:"flags,omitempty"`
}

type statusContent struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

func normalizeSpan(resource *resourcev1.Resource, resourceSchemaURL string, resourceAttrs map[string]any, scope instrumentationContent, span *tracev1.Span) (history.AppendInput, error) {
	if span == nil || len(span.GetTraceId()) != 16 || len(span.GetSpanId()) != 8 {
		return history.AppendInput{}, errors.New("rejected OTLP span with invalid 16-byte trace ID or 8-byte span ID")
	}
	if len(span.GetParentSpanId()) != 0 && len(span.GetParentSpanId()) != 8 {
		return history.AppendInput{}, fmt.Errorf("rejected OTLP span %q with invalid parent span ID", span.GetName())
	}

	spanAttrs := attributes(span.GetAttributes())
	merged := mergeAttributes(resourceAttrs, spanAttrs)
	traceHex := hex.EncodeToString(span.GetTraceId())
	spanHex := hex.EncodeToString(span.GetSpanId())
	traceID := "trace_" + traceHex
	spanID := "span_" + spanHex
	var parentID *string
	if len(span.GetParentSpanId()) == 8 {
		value := "span_" + hex.EncodeToString(span.GetParentSpanId())
		parentID = &value
	}

	start := unixNano(span.GetStartTimeUnixNano())
	end := unixNano(span.GetEndTimeUnixNano())
	var duration *float64
	if start != nil && end != nil && !end.Before(*start) {
		value := float64(end.Sub(*start)) / float64(time.Millisecond)
		duration = &value
	}
	events := make([]eventContent, 0, len(span.GetEvents()))
	for _, event := range span.GetEvents() {
		events = append(events, eventContent{
			Name: event.GetName(), Time: unixNano(event.GetTimeUnixNano()),
			Attributes: attributes(event.GetAttributes()), DroppedAttributes: event.GetDroppedAttributesCount(),
		})
	}
	links := make([]linkContent, 0, len(span.GetLinks()))
	for _, link := range span.GetLinks() {
		links = append(links, linkContent{
			TraceID: hex.EncodeToString(link.GetTraceId()), SpanID: hex.EncodeToString(link.GetSpanId()),
			TraceState: link.GetTraceState(), Attributes: attributes(link.GetAttributes()),
			DroppedAttributes: link.GetDroppedAttributesCount(), Flags: link.GetFlags(),
		})
	}
	status := statusContent{Code: statusName(span.GetStatus().GetCode()), Message: span.GetStatus().GetMessage()}
	content := spanContent{
		Schema: ContentSchema, Name: span.GetName(), Kind: spanKindName(span.GetKind()),
		StartTime: start, EndTime: end, DurationMS: duration, TraceState: span.GetTraceState(), Flags: span.GetFlags(),
		Attributes: spanAttrs,
		Resource:   resourceContent{SchemaURL: resourceSchemaURL, Attributes: resourceAttrs, DroppedAttributes: resource.GetDroppedAttributesCount()},
		Scope:      scope, Events: events, Links: links, Status: status,
		DroppedAttributes: span.GetDroppedAttributesCount(), DroppedEvents: span.GetDroppedEventsCount(), DroppedLinks: span.GetDroppedLinksCount(),
		Input: extractedValue(merged, inputKeys...), Output: extractedValue(merged, outputKeys...),
		Model:    stringValue(merged, "gen_ai.response.model", "gen_ai.request.model", "ai.response.model", "ai.model.id", "llm.model_name"),
		Provider: stringValue(merged, "gen_ai.provider.name", "gen_ai.system", "ai.model.provider", "llm.system"),
		Usage:    usage(merged),
	}
	data, err := json.Marshal(content)
	if err != nil {
		return history.AppendInput{}, fmt.Errorf("encode OTLP span %q: %w", span.GetName(), err)
	}

	conversationID := conversationID(merged, traceHex)
	kind := recordKind(span.GetName(), merged)
	agent := optional(stringValue(merged, "gen_ai.agent.name", "agent.name", "langsmith.trace.name", "ai.telemetry.functionId", "service.name"))
	tool := optional(stringValue(merged, "gen_ai.tool.name", "tool.name", "ai.toolCall.name"))
	recordStatus := optional(normalizedStatus(span, merged))
	occurredAt := start
	if occurredAt == nil {
		occurredAt = end
	}
	return history.AppendInput{
		RecordID: "otel_" + traceHex + "_" + spanHex, ConversationID: conversationID,
		Kind: kind, Content: data, OccurredAt: occurredAt, TraceID: &traceID, SpanID: &spanID, ParentID: parentID,
		Agent: agent, Tool: tool, Status: recordStatus, Tags: normalizedTags(merged, scope),
	}, nil
}

var inputKeys = []string{
	"gen_ai.input.messages", "input.value", "inputs", "ai.prompt.messages", "ai.prompt", "llm.input_messages",
}

var outputKeys = []string{
	"gen_ai.output.messages", "output.value", "outputs", "ai.response.text", "ai.response.object", "llm.output_messages",
}

func conversationID(attrs map[string]any, traceHex string) string {
	value := stringValue(attrs,
		"farfield.conversation.id", "gen_ai.conversation.id", "gen_ai.session.id", "session.id", "conversation.id", "thread.id",
		"langfuse.session.id", "langsmith.trace.session_id", "ai.telemetry.metadata.conversationId", "ai.telemetry.metadata.sessionId",
	)
	if value == "" {
		return "trace_" + traceHex
	}
	if validExternalID(value) {
		return value
	}
	digest := sha256.Sum256([]byte(value))
	return "conv_otel_" + hex.EncodeToString(digest[:])
}

func validExternalID(value string) bool {
	if len(value) == 0 || len(value) > 255 {
		return false
	}
	for index, char := range value {
		valid := char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || strings.ContainsRune("._:@/-", char)
		if !valid || index == 0 && !(char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}

func recordKind(name string, attrs map[string]any) string {
	if value := stringValue(attrs, "farfield.kind"); value != "" && len(value) <= 128 {
		return value
	}
	switch operation := strings.ToLower(stringValue(attrs, "gen_ai.operation.name")); operation {
	case "chat", "text_completion", "generate_content":
		return "model.generation"
	case "embeddings":
		return "model.embedding"
	case "execute_tool":
		return "tool.execution"
	case "retrieval":
		return "retrieval"
	case "invoke_agent":
		return "agent.invoke"
	case "create_agent":
		return "agent.create"
	case "invoke_workflow":
		return "workflow.invoke"
	case "plan":
		return "agent.plan"
	case "create_memory", "create_memory_store", "delete_memory", "delete_memory_store", "search_memory", "update_memory", "upsert_memory":
		return "memory." + operation
	}
	switch strings.ToUpper(stringValue(attrs, "openinference.span.kind")) {
	case "LLM":
		return "model.generation"
	case "EMBEDDING":
		return "model.embedding"
	case "CHAIN":
		return "workflow.invoke"
	case "RETRIEVER":
		return "retrieval"
	case "RERANKER":
		return "retrieval.rerank"
	case "TOOL":
		return "tool.execution"
	case "AGENT":
		return "agent.invoke"
	case "GUARDRAIL":
		return "guardrail"
	case "EVALUATOR":
		return "evaluation"
	case "PROMPT":
		return "prompt.render"
	}
	if value := strings.ToLower(stringValue(attrs, "langsmith.span.kind")); value != "" {
		mapping := map[string]string{"llm": "model.generation", "chain": "workflow.invoke", "tool": "tool.execution", "retriever": "retrieval", "embedding": "model.embedding", "prompt": "prompt.render", "parser": "output.parse"}
		if kind := mapping[value]; kind != "" {
			return kind
		}
	}
	operation := stringValue(attrs, "ai.operationId")
	switch {
	case operation == "ai.toolCall":
		return "tool.execution"
	case strings.HasSuffix(operation, ".doGenerate"), strings.HasSuffix(operation, ".doStream"):
		return "model.generation"
	case strings.HasSuffix(operation, ".doEmbed"):
		return "model.embedding"
	case strings.Contains(operation, "generate"), strings.Contains(operation, "stream"):
		return "agent.turn"
	case strings.Contains(operation, "embed"):
		return "embedding.batch"
	}
	lowerName := strings.ToLower(name)
	switch {
	case strings.Contains(lowerName, "tool"):
		return "tool.execution"
	case strings.Contains(lowerName, "agent"):
		return "agent.invoke"
	case strings.Contains(lowerName, "llm"), strings.Contains(lowerName, "model"), strings.Contains(lowerName, "chat"):
		return "model.generation"
	default:
		return "trace.span"
	}
}

func normalizedStatus(span *tracev1.Span, attrs map[string]any) string {
	if span.GetStatus().GetCode() == tracev1.Status_STATUS_CODE_ERROR || stringValue(attrs, "error.type") != "" {
		return "error"
	}
	if value := stringValue(attrs, "farfield.status"); value != "" {
		return value
	}
	return "ok"
}

func normalizedTags(attrs map[string]any, scope instrumentationContent) map[string]string {
	tags := map[string]string{"farfield.source": "otlp"}
	keys := []string{
		"service.name", "deployment.environment.name", "gen_ai.operation.name", "gen_ai.provider.name", "gen_ai.system",
		"gen_ai.request.model", "gen_ai.response.model", "openinference.span.kind", "langsmith.span.kind", "ai.operationId", "ai.model.provider", "ai.model.id",
	}
	for _, key := range keys {
		if value := scalarString(attrs[key]); value != "" && len(value) <= 1024 {
			tags[key] = value
		}
	}
	if scope.Name != "" {
		tags["otel.scope.name"] = scope.Name
	}
	return tags
}

func usage(attrs map[string]any) map[string]any {
	result := map[string]any{}
	for key, value := range attrs {
		if strings.HasPrefix(key, "gen_ai.usage.") ||
			strings.HasPrefix(key, "gen_ai.aggregated_usage.") ||
			strings.HasPrefix(key, "llm.token_count.") ||
			strings.HasPrefix(key, "llm.usage.") ||
			strings.HasPrefix(key, "ai.usage.") {
			result[key] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func stableSegmentID(inputs []history.AppendInput) (string, error) {
	type identity struct {
		RecordID       string            `json:"record_id"`
		ConversationID string            `json:"conversation_id"`
		Kind           string            `json:"kind"`
		Content        json.RawMessage   `json:"content"`
		OccurredAt     *time.Time        `json:"occurred_at"`
		TraceID        *string           `json:"trace_id"`
		SpanID         *string           `json:"span_id"`
		ParentID       *string           `json:"parent_id"`
		Agent          *string           `json:"agent"`
		Tool           *string           `json:"tool"`
		Status         *string           `json:"status"`
		Tags           map[string]string `json:"tags"`
	}
	values := make([]identity, 0, len(inputs))
	for _, input := range inputs {
		values = append(values, identity{
			RecordID: input.RecordID, ConversationID: input.ConversationID, Kind: input.Kind, Content: input.Content,
			OccurredAt: input.OccurredAt, TraceID: input.TraceID, SpanID: input.SpanID, ParentID: input.ParentID,
			Agent: input.Agent, Tool: input.Tool, Status: input.Status, Tags: input.Tags,
		})
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return "otlp_" + hex.EncodeToString(digest[:]), nil
}

func attributes(values []*commonv1.KeyValue) map[string]any {
	result := make(map[string]any, len(values))
	for _, value := range values {
		if value == nil || value.GetKey() == "" {
			continue
		}
		result[value.GetKey()] = anyValue(value.GetValue())
	}
	return result
}

func anyValue(value *commonv1.AnyValue) any {
	if value == nil {
		return nil
	}
	switch inner := value.GetValue().(type) {
	case *commonv1.AnyValue_StringValue:
		return inner.StringValue
	case *commonv1.AnyValue_BoolValue:
		return inner.BoolValue
	case *commonv1.AnyValue_IntValue:
		return inner.IntValue
	case *commonv1.AnyValue_DoubleValue:
		return inner.DoubleValue
	case *commonv1.AnyValue_BytesValue:
		return base64.StdEncoding.EncodeToString(inner.BytesValue)
	case *commonv1.AnyValue_ArrayValue:
		values := make([]any, 0, len(inner.ArrayValue.GetValues()))
		for _, item := range inner.ArrayValue.GetValues() {
			values = append(values, anyValue(item))
		}
		return values
	case *commonv1.AnyValue_KvlistValue:
		return attributes(inner.KvlistValue.GetValues())
	default:
		return nil
	}
}

func scopeContent(scope *commonv1.InstrumentationScope, schemaURL string) instrumentationContent {
	if scope == nil {
		return instrumentationContent{SchemaURL: schemaURL}
	}
	return instrumentationContent{
		Name: scope.GetName(), Version: scope.GetVersion(), SchemaURL: schemaURL, Attrs: attributes(scope.GetAttributes()),
	}
}

func mergeAttributes(resource, span map[string]any) map[string]any {
	result := make(map[string]any, len(resource)+len(span))
	for key, value := range resource {
		result[key] = value
	}
	for key, value := range span {
		result[key] = value
	}
	return result
}

func extractedValue(attrs map[string]any, keys ...string) any {
	for _, key := range keys {
		value, found := attrs[key]
		if !found {
			continue
		}
		if text, ok := value.(string); ok {
			var decoded any
			if json.Unmarshal([]byte(text), &decoded) == nil {
				return decoded
			}
		}
		return value
	}
	return nil
}

func stringValue(attrs map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := scalarString(attrs[key]); value != "" {
			return value
		}
	}
	return ""
}

func scalarString(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case bool:
		return fmt.Sprintf("%t", value)
	case int64:
		return fmt.Sprintf("%d", value)
	case float64:
		return fmt.Sprintf("%g", value)
	default:
		return ""
	}
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func unixNano(value uint64) *time.Time {
	if value == 0 || value > uint64(^uint64(0)>>1) {
		return nil
	}
	result := time.Unix(0, int64(value)).UTC()
	return &result
}

func spanKindName(value tracev1.Span_SpanKind) string {
	return strings.TrimPrefix(value.String(), "SPAN_KIND_")
}

func statusName(value tracev1.Status_StatusCode) string {
	return strings.TrimPrefix(value.String(), "STATUS_CODE_")
}
