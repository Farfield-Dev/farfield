# HTTP API

The local server exposes a deliberately small `/v1` API. The canonical machine-readable description is [`openapi.yaml`](../openapi.yaml).

```text
GET  /v1/health
POST /v1/history/records
POST /v1/history/segments
GET  /v1/history/records
GET  /v1/history/records/{record_id}
GET  /v1/history/conversations
GET  /v1/history/conversations/{conversation_id}/timeline
POST /v1/runtime/runs
GET  /v1/runtime/runs/{run_id}
GET  /v1/runtime/runs/{run_id}/events
POST /v1/runtime/runs/{run_id}/transitions
POST /v1/runtime/runs/{run_id}/checkpoints
```

`POST /v1/history/segments` is the batch-oriented durable ingestion path. Its
records must share a conversation ID. Small canonical JSON content is embedded
in the segment; larger values are committed as content-addressed blobs before
the segment. The segment object is the acknowledgment boundary.

Runtime mutations require an `operation_id`. Retrying identical input with the
same operation ID returns the committed event. A different concurrent writer
for the same next sequence receives `FR_CONCURRENT_TRANSITION` and must reload
the run before deciding what to do. Reads verify the event hash chain before
returning reconstructed state.

The API currently has no authentication or tenant isolation. The CLI therefore binds to `127.0.0.1` by default. Put an authenticated proxy in front of evaluation deployments and do not expose it directly to the internet.

Errors use stable codes:

```json
{
  "error": {
    "code": "FH_IDEMPOTENCY_CONFLICT",
    "message": "record id was reused for different content",
    "retryable": false
  }
}
```

Clients should branch on `code`, not human-readable message text.
