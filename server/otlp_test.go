package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Farfield-Dev/farfield/history"
	"github.com/Farfield-Dev/farfield/storage"
	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestOTLPHTTPProtobufJSONAndGzip(t *testing.T) {
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := history.New(store)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(service)
	if err != nil {
		t.Fatal(err)
	}
	span := &tracev1.Span{
		TraceId: repeatByte(16, 1), SpanId: repeatByte(8, 2), Name: "invoke agent",
		Attributes: []*commonv1.KeyValue{{Key: "gen_ai.operation.name", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "invoke_agent"}}}},
	}
	request := &collectortracev1.ExportTraceServiceRequest{ResourceSpans: []*tracev1.ResourceSpans{
		{ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{span}}}},
	}}

	tests := []struct {
		name        string
		path        string
		contentType string
		encoding    string
		marshal     func() []byte
	}{
		{"protobuf", "/v1/traces", "application/x-protobuf", "", func() []byte { value, _ := proto.Marshal(request); return value }},
		{"json gzip", "/v1/otel/v1/traces", "application/json", "gzip", func() []byte { value, _ := protojson.Marshal(request); return gzipBytes(t, value) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			httpRequest := httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(test.marshal()))
			httpRequest.Header.Set("Content-Type", test.contentType)
			if test.encoding != "" {
				httpRequest.Header.Set("Content-Encoding", test.encoding)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httpRequest)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
	timeline, err := service.Timeline(context.Background(), "trace_01010101010101010101010101010101")
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) != 1 || timeline[0].Record.Kind != "agent.invoke" {
		t.Fatalf("timeline = %#v", timeline)
	}
}

func TestOTLPHTTPReturnsPartialSuccess(t *testing.T) {
	store, _ := storage.OpenLocal(t.TempDir())
	service, _ := history.New(store)
	server, _ := New(service)
	span := &tracev1.Span{TraceId: []byte{1}, SpanId: []byte{2}, Name: "invalid"}
	request := &collectortracev1.ExportTraceServiceRequest{ResourceSpans: []*tracev1.ResourceSpans{
		{ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{span}}}},
	}}
	body, _ := proto.Marshal(request)
	httpRequest := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(body))
	httpRequest.Header.Set("Content-Type", "application/x-protobuf")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httpRequest)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	decoded := &collectortracev1.ExportTraceServiceResponse{}
	if err := proto.Unmarshal(response.Body.Bytes(), decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.GetPartialSuccess().GetRejectedSpans() != 1 {
		t.Fatalf("response = %#v", decoded)
	}
}

func gzipBytes(t *testing.T, input []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(input); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func repeatByte(length int, value byte) []byte {
	return bytes.Repeat([]byte{value}, length)
}
