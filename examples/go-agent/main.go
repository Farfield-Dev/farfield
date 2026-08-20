package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Farfield-Dev/farfield/history"
	"github.com/Farfield-Dev/farfield/storage"
)

func main() {
	ctx := context.Background()
	store, err := storage.OpenLocal(".farfield/objects")
	if err != nil {
		log.Fatal(err)
	}
	recorder, err := history.New(store)
	if err != nil {
		log.Fatal(err)
	}
	conversationID := "conv_go_example"
	for _, event := range []history.AppendInput{
		{ConversationID: conversationID, Kind: "user.message", Content: []byte(`{"text":"What changed in the repository?"}`)},
		{ConversationID: conversationID, Kind: "tool.call", Tool: ptr("git.diff"), Content: []byte(`{"base":"main"}`)},
		{ConversationID: conversationID, Kind: "tool.result", Tool: ptr("git.diff"), Status: ptr("completed"), Content: []byte(`{"files":7,"insertions":312}`)},
		{ConversationID: conversationID, Kind: "model.response", Content: []byte(`{"text":"Seven files changed."}`)},
	} {
		if _, err := recorder.Append(ctx, event); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Println("Captured", conversationID)
	fmt.Println("Inspect it with: go run ./cmd/farfield serve")
}

func ptr(value string) *string { return &value }
