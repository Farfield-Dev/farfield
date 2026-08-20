# Farfield Go SDK

The Go SDK is a zero-dependency HTTP client for Farfield History and Runtime.
Successful mutation calls are durable acknowledgments, not local queue accepts.

```go
client, err := farfield.New(
    farfield.WithEndpoint("http://127.0.0.1:8787"),
    farfield.WithDefaults(farfield.Scope{
        Agent: "researcher",
        Tags: map[string]string{"environment": "development"},
    }),
)
if err != nil {
    log.Fatal(err)
}

ctx := farfield.WithConversation(context.Background(), "conv_123")
record, err := client.Capture(ctx, farfield.CaptureInput{
    Kind: "model.response",
    Content: map[string]any{"model": "gpt-5", "text": "Hello"},
})
```

Batch a completed turn into one durable segment:

```go
segment, err := client.Conversation("conv_123").CaptureBatch(ctx, []farfield.CaptureInput{
    {Kind: "message.input", Content: map[string]any{"text": "Hello"}},
    {Kind: "message.output", Content: map[string]any{"text": "Hi"}},
})
```

Use `client.NewConversation()` when Farfield should generate the conversation
ID, or `client.Conversation("conv_123")` when your application already owns it.
The returned helper exposes the resolved value through `ID()`.

Read the data back without dropping down to raw HTTP:

```go
timeline, err := client.Timeline(ctx, "conv_123")
records, err := client.Query(ctx, farfield.HistoryQuery{
    Agent: "researcher",
    Kind:  "tool.result",
    Limit: 50,
})
conversations, err := client.Conversations(ctx, 20)
```

Journal a run managed by your worker:

```go
created, err := client.CreateRun(ctx, farfield.CreateRunInput{
    ID: "run_123",
    Checkpoint: map[string]any{"next": "research"},
})

_, err = client.TransitionRun(ctx, created.RunID, farfield.TransitionRunInput{
    To: farfield.Running,
})
```

Configuration defaults to `FARFIELD_ENDPOINT=http://127.0.0.1:8787` and reads
an optional `FARFIELD_TOKEN`. Use `WithBeforeSend` to redact or reject content
before encoding and transport. Generated record, segment, run, and operation
IDs remain stable across automatic retries.
