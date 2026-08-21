package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Farfield-Dev/farfield/history"
	"github.com/Farfield-Dev/farfield/storage"
)

func TestHistoryHTTPRoundTrip(t *testing.T) {
	t.Parallel()
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
	body := []byte(`{"id":"rec_http","conversation_id":"conv_http","kind":"model.response","content":{"text":"hello"}}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/history/records", bytes.NewReader(body))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("append status = %d; body = %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/history/conversations/conv_http/timeline", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("timeline status = %d; body = %s", response.Code, response.Body.String())
	}
	var timeline []history.Entry
	if err := json.Unmarshal(response.Body.Bytes(), &timeline); err != nil {
		t.Fatal(err)
	}
	if len(timeline) != 1 || timeline[0].Record.ID != "rec_http" || string(timeline[0].Content) != `{"text":"hello"}` {
		t.Fatalf("timeline = %#v", timeline)
	}
}

func TestSegmentHTTPRoundTrip(t *testing.T) {
	t.Parallel()
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
	body := []byte(`{"id":"seg_http","records":[{"id":"rec_one","conversation_id":"conv_http","kind":"message.input","content":{"text":"hello"}},{"id":"rec_two","conversation_id":"conv_http","kind":"message.output","content":{"text":"hi"}}]}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/history/segments", bytes.NewReader(body))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("append segment status = %d; body = %s", response.Code, response.Body.String())
	}
	var segment history.Segment
	if err := json.Unmarshal(response.Body.Bytes(), &segment); err != nil {
		t.Fatal(err)
	}
	if segment.ID != "seg_http" || len(segment.Entries) != 2 {
		t.Fatalf("segment = %#v", segment)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/history/conversations/conv_http/timeline", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("timeline status = %d; body = %s", response.Code, response.Body.String())
	}
	var timeline []history.Entry
	if err := json.Unmarshal(response.Body.Bytes(), &timeline); err != nil {
		t.Fatal(err)
	}
	if len(timeline) != 2 || timeline[1].Record.ID != "rec_two" {
		t.Fatalf("timeline = %#v", timeline)
	}
}

func TestHealthAndUI(t *testing.T) {
	t.Parallel()
	store, _ := storage.OpenLocal(t.TempDir())
	service, _ := history.New(store)
	server, _ := New(service)
	for _, path := range []string{"/", "/v1/health"} {
		request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(context.Background())
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/missing-ui-asset.js", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing UI asset = %d", response.Code)
	}
}
