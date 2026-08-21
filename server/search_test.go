package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Farfield-Dev/farfield/history"
	"github.com/Farfield-Dev/farfield/storage"
)

func TestSearchEndpointReturnsRankedFilteredHits(t *testing.T) {
	t.Parallel()
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := history.New(store)
	if err != nil {
		t.Fatal(err)
	}
	agent := "researcher"
	if _, err := service.Append(context.Background(), history.AppendInput{
		RecordID: "rec_http_search", ConversationID: "conv_http_search", Kind: "message.assistant",
		Agent: &agent, Tags: map[string]string{"env": "test"}, Content: []byte(`{"text":"object storage search is fast"}`),
	}); err != nil {
		t.Fatal(err)
	}
	handler, err := New(service)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/history/search?q=object+storage&agent=researcher&tag=env%3Dtest&limit=10", nil)
	response := httptest.NewRecorder()
	handler.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("search status = %d; body = %s", response.Code, response.Body.String())
	}
	var result history.SearchResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || len(result.Hits) != 1 || result.Hits[0].Record.ID != "rec_http_search" {
		t.Fatalf("search result = %#v", result)
	}
}

func TestSearchEndpointRejectsInvalidTag(t *testing.T) {
	t.Parallel()
	store, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := history.New(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(service)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/history/search?q=test&tag=invalid", nil)
	response := httptest.NewRecorder()
	handler.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("search status = %d; body = %s", response.Code, response.Body.String())
	}
}
