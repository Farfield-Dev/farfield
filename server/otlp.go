package server

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const maxOTLPRequestBytes = 32 * 1024 * 1024

func (server *Server) exportOTLPTraces(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxOTLPRequestBytes)
	reader := io.Reader(request.Body)
	if encoding := strings.TrimSpace(strings.ToLower(request.Header.Get("Content-Encoding"))); encoding != "" {
		if encoding != "gzip" {
			writeError(writer, http.StatusUnsupportedMediaType, "FH_OTLP_ENCODING_UNSUPPORTED", "OTLP Content-Encoding must be gzip or omitted", nil)
			return
		}
		compressed, err := gzip.NewReader(request.Body)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "FH_OTLP_INVALID", "OTLP gzip body is invalid", err)
			return
		}
		defer compressed.Close()
		reader = io.LimitReader(compressed, maxOTLPRequestBytes+1)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(writer, http.StatusRequestEntityTooLarge, "FH_OTLP_TOO_LARGE", "OTLP request exceeds 32 MiB", nil)
			return
		}
		writeError(writer, http.StatusBadRequest, "FH_OTLP_INVALID", "OTLP request body could not be read", err)
		return
	}
	if len(body) > maxOTLPRequestBytes {
		writeError(writer, http.StatusRequestEntityTooLarge, "FH_OTLP_TOO_LARGE", "OTLP request exceeds 32 MiB", nil)
		return
	}

	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil {
		writeError(writer, http.StatusUnsupportedMediaType, "FH_OTLP_CONTENT_TYPE_UNSUPPORTED", "OTLP Content-Type is invalid", err)
		return
	}
	value := &collectortracev1.ExportTraceServiceRequest{}
	switch mediaType {
	case "application/x-protobuf", "application/protobuf", "application/octet-stream":
		err = proto.Unmarshal(body, value)
	case "application/json":
		err = protojson.UnmarshalOptions{DiscardUnknown: false}.Unmarshal(body, value)
	default:
		writeError(writer, http.StatusUnsupportedMediaType, "FH_OTLP_CONTENT_TYPE_UNSUPPORTED", "OTLP Content-Type must be application/x-protobuf or application/json", nil)
		return
	}
	if err != nil {
		writeError(writer, http.StatusBadRequest, "FH_OTLP_INVALID", "OTLP trace export is invalid", err)
		return
	}

	result, err := server.otlp.Export(request.Context(), value.GetResourceSpans())
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "FH_OTLP_COMMIT_FAILED", "OTLP spans could not be durably committed", err)
		return
	}
	response := &collectortracev1.ExportTraceServiceResponse{}
	if result.Rejected > 0 {
		message := strings.Join(result.Errors, "; ")
		if len(message) > 1024 {
			message = message[:1021] + "..."
		}
		response.PartialSuccess = &collectortracev1.ExportTracePartialSuccess{
			RejectedSpans: int64(result.Rejected),
			ErrorMessage:  fmt.Sprintf("Farfield accepted %d spans; %s", result.Accepted, message),
		}
	}

	writer.Header().Set("Content-Type", mediaType)
	writer.WriteHeader(http.StatusOK)
	if mediaType == "application/json" {
		data, marshalErr := protojson.MarshalOptions{UseProtoNames: false}.Marshal(response)
		if marshalErr == nil {
			_, _ = writer.Write(data)
		}
		return
	}
	data, marshalErr := proto.Marshal(response)
	if marshalErr == nil {
		_, _ = writer.Write(data)
	}
}
