# Quickstart

## Run locally

```bash
git clone https://github.com/Farfield-Dev/farfield.git
cd farfield
go run ./examples/go-agent
go run ./cmd/farfield serve
```

The example writes four immutable events into `.farfield/objects`. The server reads those authoritative objects directly and exposes the inspector at `http://127.0.0.1:8787`.

No database or account is required.

## Capture an event

```bash
curl -sS -X POST http://127.0.0.1:8787/v1/history/records \
  -H 'content-type: application/json' \
  -d '{
    "id": "rec_my_stable_retry_key",
    "conversation_id": "conv_support_123",
    "kind": "tool.result",
    "tool": "lookup_order",
    "status": "completed",
    "tags": {"environment": "development"},
    "content": {"order_id": "ord_42", "status": "shipped"}
  }'
```

Supplying a stable `id` makes retries idempotent. Reusing that ID with different content returns a conflict; Farfield never overwrites the original bytes.

## Capture a durable batch

SDKs should normally coalesce completed messages, tool calls, and other
semantic events into a segment. Every record in this request becomes durable
with one immutable segment commit:

```bash
curl -sS -X POST http://127.0.0.1:8787/v1/history/segments \
  -H 'content-type: application/json' \
  -d '{
    "id": "seg_support_123_turn_1",
    "records": [
      {
        "id": "rec_support_input",
        "conversation_id": "conv_support_123",
        "kind": "message.input",
        "content": {"text": "Where is my order?"}
      },
      {
        "id": "rec_support_tool",
        "conversation_id": "conv_support_123",
        "kind": "tool.result",
        "tool": "lookup_order",
        "status": "completed",
        "content": {"order_id": "ord_42", "status": "shipped"}
      }
    ]
  }'
```

All records in a segment belong to one conversation. A stable segment `id`
makes the entire batch idempotent, including recovery when a client loses the
response after the object was committed.

## Inspect and verify

```bash
go run ./cmd/farfield history conversations
go run ./cmd/farfield history query --kind tool.result
go run ./cmd/farfield history timeline --conversation conv_support_123
go run ./cmd/farfield history verify
```

`verify` recomputes segment, record, and payload checksums and reports missing,
corrupt, duplicate, and orphaned objects.

Conversation reads use projection objects only. To deliberately reconstruct
the conversation projection from authoritative History, run:

```bash
go run ./cmd/farfield history projections rebuild
```

## Use S3-compatible storage

```bash
export AWS_REGION=us-east-1
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...

go run ./cmd/farfield serve \
  --store s3://my-bucket/farfield/dev \
  --listen 127.0.0.1:8787
```

For a custom endpoint:

```bash
export FARFIELD_S3_ENDPOINT=https://example.r2.cloudflarestorage.com
export FARFIELD_S3_REGION=auto
export FARFIELD_S3_PATH_STYLE=true
```

The endpoint must implement atomic conditional `PutObject` using `If-None-Match: *`. Farfield rejects endpoints that cannot enforce immutable creation.

## Use Google Cloud Storage

Farfield uses the native GCS API and Application Default Credentials. A local
developer can authenticate with the Google Cloud CLI, while production
workloads normally inherit a service-account identity from their environment:

```bash
gcloud auth application-default login

go run ./cmd/farfield serve \
  --store gs://my-bucket/farfield/dev \
  --listen 127.0.0.1:8787
```

Every immutable create is committed with `ifGenerationMatch=0`. Concurrent
writers therefore cannot overwrite one another, and a lost response can be
recovered by repeating the exact write.

The [Python personal-agent example](../examples/python-personal-agent/README.md)
is a complete GCS-backed integration: it runs live Anthropic web research,
captures provider and semantic events, and queries the retained trace through
the SDK, CLI, API, and embedded inspector.
