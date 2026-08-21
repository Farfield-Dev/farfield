package otlp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Farfield-Dev/farfield/history"
	"github.com/Farfield-Dev/farfield/storage"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

func TestExportNormalizesAgentSpansAndRetries(t *testing.T) {
	service := testHistory(t)
	ingestor, err := New(service)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 8, 20, 20, 0, 0, 0, time.UTC)
	traceID := bytesOf(16, 1)
	modelID := bytesOf(8, 2)
	toolID := bytesOf(8, 3)
	request := []*tracev1.ResourceSpans{{
		Resource: &resourcev1.Resource{Attributes: []*commonv1.KeyValue{
			stringAttribute("service.name", "research-agent"),
		}},
		ScopeSpans: []*tracev1.ScopeSpans{{
			Scope: &commonv1.InstrumentationScope{Name: "test.instrumentation", Version: "1.2.3"},
			Spans: []*tracev1.Span{
				{
					TraceId: traceID, SpanId: modelID, Name: "chat claude", Kind: tracev1.Span_SPAN_KIND_CLIENT,
					StartTimeUnixNano: uint64(start.UnixNano()), EndTimeUnixNano: uint64(start.Add(250 * time.Millisecond).UnixNano()),
					Attributes: []*commonv1.KeyValue{
						stringAttribute("session.id", "conv_research"), stringAttribute("gen_ai.operation.name", "chat"),
						stringAttribute("gen_ai.agent.name", "researcher"), stringAttribute("gen_ai.provider.name", "anthropic"),
						stringAttribute("gen_ai.request.model", "claude-sonnet"),
						stringAttribute("gen_ai.input.messages", `[{"role":"user","content":"research object storage"}]`),
						intAttribute("gen_ai.usage.input_tokens", 42),
					},
					Status: &tracev1.Status{Code: tracev1.Status_STATUS_CODE_OK},
				},
				{
					TraceId: traceID, SpanId: toolID, ParentSpanId: modelID, Name: "execute_tool browser", Kind: tracev1.Span_SPAN_KIND_INTERNAL,
					StartTimeUnixNano: uint64(start.Add(10 * time.Millisecond).UnixNano()), EndTimeUnixNano: uint64(start.Add(100 * time.Millisecond).UnixNano()),
					Attributes: []*commonv1.KeyValue{
						stringAttribute("session.id", "conv_research"), stringAttribute("gen_ai.operation.name", "execute_tool"),
						stringAttribute("gen_ai.tool.name", "browser.search"), stringAttribute("output.value", `{"results":3}`),
					},
					Status: &tracev1.Status{Code: tracev1.Status_STATUS_CODE_OK},
				},
			},
		}},
	}}

	for attempt := 0; attempt < 2; attempt++ {
		result, exportErr := ingestor.Export(context.Background(), request)
		if exportErr != nil {
			t.Fatalf("attempt %d: %v", attempt, exportErr)
		}
		if result.Accepted != 2 || result.Rejected != 0 {
			t.Fatalf("result = %#v", result)
		}
	}

	timeline, err := service.Timeline(context.Background(), "conv_research")
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) != 2 {
		t.Fatalf("timeline length = %d, want 2", len(timeline))
	}
	if timeline[0].Record.Kind != "model.generation" || value(timeline[0].Record.Agent) != "researcher" {
		t.Fatalf("model record = %#v", timeline[0].Record)
	}
	if timeline[1].Record.Kind != "tool.execution" || value(timeline[1].Record.Tool) != "browser.search" || value(timeline[1].Record.ParentID) != "span_0202020202020202" {
		t.Fatalf("tool record = %#v", timeline[1].Record)
	}
	var content map[string]any
	if err := json.Unmarshal(timeline[0].Content, &content); err != nil {
		t.Fatal(err)
	}
	if content["schema"] != ContentSchema || content["model"] != "claude-sonnet" || content["provider"] != "anthropic" {
		t.Fatalf("content = %#v", content)
	}
	input := content["input"].([]any)
	if input[0].(map[string]any)["role"] != "user" {
		t.Fatalf("decoded input = %#v", input)
	}
}

func TestExportMapsPopularConventions(t *testing.T) {
	service := testHistory(t)
	ingestor, _ := New(service)
	tests := []struct {
		name  string
		attrs []*commonv1.KeyValue
		want  string
	}{
		{"OpenInference LlamaIndex", []*commonv1.KeyValue{stringAttribute("openinference.span.kind", "RETRIEVER")}, "retrieval"},
		{"LangSmith LangGraph", []*commonv1.KeyValue{stringAttribute("langsmith.span.kind", "chain")}, "workflow.invoke"},
		{"PydanticAI", []*commonv1.KeyValue{stringAttribute("gen_ai.operation.name", "invoke_agent"), intAttribute("gen_ai.aggregated_usage.input_tokens", 12)}, "agent.invoke"},
		{"AutoGen", []*commonv1.KeyValue{stringAttribute("gen_ai.operation.name", "execute_tool"), stringAttribute("gen_ai.tool.name", "python")}, "tool.execution"},
		{"Google ADK", []*commonv1.KeyValue{stringAttribute("gen_ai.operation.name", "invoke_agent"), stringAttribute("gen_ai.agent.name", "planner")}, "agent.invoke"},
		{"Strands Agents", []*commonv1.KeyValue{stringAttribute("gen_ai.operation.name", "chat"), stringAttribute("gen_ai.provider.name", "aws.bedrock")}, "model.generation"},
		{"Mastra", []*commonv1.KeyValue{stringAttribute("gen_ai.operation.name", "invoke_workflow")}, "workflow.invoke"},
		{"Semantic Kernel", []*commonv1.KeyValue{stringAttribute("gen_ai.operation.name", "chat"), stringAttribute("gen_ai.request.model", "gpt-test")}, "model.generation"},
		{"CrewAI instrumentor", []*commonv1.KeyValue{stringAttribute("gen_ai.operation.name", "invoke_agent"), stringAttribute("gen_ai.agent.name", "researcher")}, "agent.invoke"},
		{"OpenInference Agno", []*commonv1.KeyValue{stringAttribute("openinference.span.kind", "AGENT")}, "agent.invoke"},
		{"OpenInference AG2", []*commonv1.KeyValue{stringAttribute("openinference.span.kind", "AGENT")}, "agent.invoke"},
		{"OpenInference DSPy", []*commonv1.KeyValue{stringAttribute("openinference.span.kind", "LLM"), stringAttribute("llm.model_name", "gpt-test")}, "model.generation"},
		{"OpenInference Haystack", []*commonv1.KeyValue{stringAttribute("openinference.span.kind", "CHAIN")}, "workflow.invoke"},
		{"OpenInference smolagents", []*commonv1.KeyValue{stringAttribute("openinference.span.kind", "AGENT")}, "agent.invoke"},
		{"OpenInference BeeAI", []*commonv1.KeyValue{stringAttribute("openinference.span.kind", "AGENT")}, "agent.invoke"},
		{"OpenInference TanStack AI", []*commonv1.KeyValue{stringAttribute("openinference.span.kind", "LLM")}, "model.generation"},
		{"OpenInference Bedrock Agent Runtime", []*commonv1.KeyValue{stringAttribute("openinference.span.kind", "AGENT")}, "agent.invoke"},
		{"OpenInference MCP", []*commonv1.KeyValue{stringAttribute("openinference.span.kind", "TOOL")}, "tool.execution"},
		{"OpenInference Spring AI", []*commonv1.KeyValue{stringAttribute("openinference.span.kind", "LLM")}, "model.generation"},
		{"OpenInference LangChain4j", []*commonv1.KeyValue{stringAttribute("openinference.span.kind", "CHAIN")}, "workflow.invoke"},
		{"Vercel AI SDK model", []*commonv1.KeyValue{stringAttribute("ai.operationId", "ai.streamText.doStream")}, "model.generation"},
		{"Vercel AI SDK tool", []*commonv1.KeyValue{stringAttribute("ai.operationId", "ai.toolCall"), stringAttribute("ai.toolCall.name", "weather")}, "tool.execution"},
	}
	for index, test := range tests {
		spanID := bytesOf(8, byte(index+1))
		result, err := ingestor.Export(context.Background(), []*tracev1.ResourceSpans{{ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{{
			TraceId: bytesOf(16, 9), SpanId: spanID, Name: test.name, Attributes: test.attrs,
		}}}}}})
		if err != nil || result.Accepted != 1 {
			t.Fatalf("%s export = %#v, %v", test.name, result, err)
		}
	}
	timeline, err := service.Timeline(context.Background(), "trace_09090909090909090909090909090909")
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) != len(tests) {
		t.Fatalf("timeline length = %d", len(timeline))
	}
	got := map[string]string{}
	for _, entry := range timeline {
		var content struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(entry.Content, &content); err != nil {
			t.Fatal(err)
		}
		got[content.Name] = entry.Record.Kind
	}
	for _, test := range tests {
		if got[test.name] != test.want {
			t.Errorf("%s kind = %q, want %q", test.name, got[test.name], test.want)
		}
	}
}

func TestExportPreservesResourceSchemaAndAggregatedUsage(t *testing.T) {
	service := testHistory(t)
	ingestor, _ := New(service)
	_, err := ingestor.Export(context.Background(), []*tracev1.ResourceSpans{{
		SchemaUrl: "https://opentelemetry.io/schemas/1.37.0",
		ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{{
			TraceId: bytesOf(16, 7), SpanId: bytesOf(8, 8), Name: "PydanticAI agent run",
			Attributes: []*commonv1.KeyValue{
				stringAttribute("gen_ai.operation.name", "invoke_agent"),
				intAttribute("gen_ai.aggregated_usage.input_tokens", 21),
				intAttribute("gen_ai.aggregated_usage.output_tokens", 13),
			},
		}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	timeline, err := service.Timeline(context.Background(), "trace_07070707070707070707070707070707")
	if err != nil {
		t.Fatal(err)
	}
	var content struct {
		Resource struct {
			SchemaURL string `json:"schema_url"`
		} `json:"resource"`
		Usage map[string]any `json:"usage"`
	}
	if err := json.Unmarshal(timeline[0].Content, &content); err != nil {
		t.Fatal(err)
	}
	if content.Resource.SchemaURL != "https://opentelemetry.io/schemas/1.37.0" {
		t.Fatalf("resource schema = %q", content.Resource.SchemaURL)
	}
	if content.Usage["gen_ai.aggregated_usage.input_tokens"] != float64(21) {
		t.Fatalf("usage = %#v", content.Usage)
	}
}

func TestExportRejectsInvalidSpanWithoutLosingValidSpan(t *testing.T) {
	service := testHistory(t)
	ingestor, _ := New(service)
	result, err := ingestor.Export(context.Background(), []*tracev1.ResourceSpans{{ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{
		{Name: "invalid", TraceId: []byte{1}, SpanId: []byte{2}},
		{Name: "valid", TraceId: bytesOf(16, 3), SpanId: bytesOf(8, 4)},
	}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted != 1 || result.Rejected != 1 || len(result.Errors) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func testHistory(t *testing.T) *history.Service {
	t.Helper()
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := history.New(store)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func stringAttribute(key, value string) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: value}}}
}

func intAttribute(key string, value int64) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_IntValue{IntValue: value}}}
}

func bytesOf(length int, value byte) []byte {
	result := make([]byte, length)
	for index := range result {
		result[index] = value
	}
	return result
}

func value(input *string) string {
	if input == nil {
		return ""
	}
	return *input
}
