package main

import (
	"context"
	"fmt"
	"log"
	"os"

	farfield "github.com/Farfield-Dev/farfield/sdk/go"
)

func main() {
	conversationID := os.Getenv("FARFIELD_CONVERSATION")
	if conversationID == "" {
		conversationID = "conv_sdk_smoke"
	}
	client, err := farfield.New(farfield.WithDefaults(farfield.Scope{Agent: "go-sdk-smoke"}))
	if err != nil {
		log.Fatal(err)
	}
	ctx := farfield.WithConversation(context.Background(), conversationID)
	record, err := client.Capture(ctx, farfield.CaptureInput{
		Kind: "test.sdk.go",
		Content: map[string]any{
			"message": "written through the public Go SDK",
		},
		Status: "completed",
	})
	if err != nil {
		log.Fatal(err)
	}
	timeline, err := client.Timeline(ctx, conversationID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("record=%s conversation=%s timeline_records=%d\n", record.ID, conversationID, len(timeline))
}
