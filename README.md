# Farfield

[![CI](https://github.com/Farfield-Dev/farfield/actions/workflows/ci.yml/badge.svg)](https://github.com/Farfield-Dev/farfield/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

Farfield makes object storage the durable source of truth for agent history and execution.

The project is building an open substrate for long-running agents: durable execution, complete history, replay, debugging, and observability without requiring a proprietary data silo. Authoritative records live in storage you control. Indexes, dashboards, and analytics are projections that can be rebuilt.

> **Status:** pre-release. Immutable History and the first durable Runtime
> journal work end to end. Worker scheduling, signals, timers, leases, and
> automatic resumption are still roadmap work—not production-ready claims.

## What works now

- Append canonical JSON events to an immutable local object store.
- Commit many events in one checksummed, idempotent history segment.
- Keep small event content inline and move larger content to addressed blobs.
- Store payloads once by SHA-256 and reference them from sealed records.
- Retry appends safely with stable record IDs.
- Detect record, payload, and immutable-write corruption.
- Read a record and its payload without a database index.
- Verify the authoritative store and identify orphaned blobs.
- Use the same storage interface for local files and S3-compatible storage.
- Run everything through one `farfield` CLI binary.
- Query records by conversation, trace, kind, agent, tool, or status.
- Inspect conversation summaries and hydrated timelines.
- Ingest and query over a versioned HTTP API.
- Browse captured conversations in the embedded local inspector.
- Create durable runs without a database or mutable state row.
- Commit validated run transitions and canonical JSON checkpoints.
- Retry every runtime operation with a stable idempotency key.
- Reconstruct and verify attempt counts, current state, and the complete
  hash-chained run journal.
- Serialize concurrent run writers with one atomic object-store operation.

## Try it in two minutes

Requires Go 1.24 or newer.

```bash
git clone https://github.com/Farfield-Dev/farfield.git
cd farfield
go run ./examples/go-agent
go run ./cmd/farfield serve
```

Open [http://127.0.0.1:8787](http://127.0.0.1:8787) to inspect the captured conversation and its hydrated event timeline.

You can also capture from any language over HTTP. The segment endpoint is the
preferred ingestion path for SDKs because one object commit can make multiple
records durable:

```bash
curl -X POST http://127.0.0.1:8787/v1/history/segments \
  -H 'content-type: application/json' \
  -d '{
    "id": "seg_demo_turn_1",
    "records": [
      {
        "id": "rec_demo_input",
        "conversation_id": "conv_demo",
        "kind": "message.input",
        "content": {"text": "hello"}
      },
      {
        "id": "rec_demo_output",
        "conversation_id": "conv_demo",
        "kind": "message.output",
        "agent": "researcher",
        "content": {"model": "gpt-5", "text": "hi"}
      }
    ]
  }'
```

Single-record capture remains available:

```bash
curl -X POST http://127.0.0.1:8787/v1/history/records \
  -H 'content-type: application/json' \
  -d '{
    "conversation_id": "conv_demo",
    "kind": "model.response",
    "agent": "researcher",
    "content": {"model": "gpt-5", "text": "hello"}
  }'
```

Or use the CLI without running a server:

```bash
go run ./cmd/farfield history append \
  --conversation conv_demo \
  --kind model.response \
  --content '{"model":"gpt-5","text":"hello"}'

go run ./cmd/farfield history timeline --conversation conv_demo
go run ./cmd/farfield history verify

go run ./cmd/farfield runtime create \
  --id run_demo --operation create --checkpoint '{"step":0}'
go run ./cmd/farfield runtime transition \
  --run run_demo --operation start --to running
go run ./cmd/farfield runtime checkpoint \
  --run run_demo --operation save_1 --checkpoint '{"step":1}'
go run ./cmd/farfield runtime get --run run_demo
go run ./cmd/farfield runtime verify
```

An S3-compatible store uses the same commands:

```bash
farfield history verify --store s3://my-bucket/farfield
```

AWS credentials use the standard AWS SDK credential chain. Set `FARFIELD_S3_ENDPOINT` for a compatible endpoint such as MinIO or R2, and `FARFIELD_S3_PATH_STYLE=true` when the provider requires path-style addressing.

## Install

From a tagged release:

```bash
go install github.com/Farfield-Dev/farfield/cmd/farfield@latest
```

Or build the self-contained container locally:

```bash
docker build --build-arg VERSION=dev -t farfield .
docker run --rm -p 8787:8787 -v "$PWD/.farfield:/data" farfield
```

## Architecture

```text
Python / TypeScript / Go SDKs
        framework-native capture and workers
                         │
                versioned protocols
                         ▼
                   Farfield core
       ingest · history · runtime · query · replay
                         │
                         ▼
              S3-compatible object storage
                   durable authority
                         │
                         ▼
          disposable indexes and product views
```

The Go core owns storage semantics, ingestion, querying, runtime coordination, recovery, and the CLI/server. SDKs stay native to the language where an agent executes. The persisted protocol—not a Go API—is the platform boundary.

See [docs/architecture.md](docs/architecture.md) for package boundaries,
[docs/design](docs/design/README.md) for substantial design proposals, and
[ROADMAP.md](ROADMAP.md) for the path from this bootstrap to durable agent
execution.

## Current boundaries

This first release is useful for local evaluation, SDK development, and proving the storage protocol. It is not yet a production observability backend:

- Queries currently scan authoritative records and relevant conversation shards; manifests and a rebuildable query projection are next.
- The HTTP server has no authentication or tenant isolation and binds to loopback by default.
- S3 immutable writes require `PutObject` with `If-None-Match: *`; incompatible providers are rejected.
- Framework-native Python and TypeScript capture SDKs are planned but not included yet.
- Runtime durably journals run state and checkpoints but does not yet schedule
  workers, wake timers, deliver signals, fence leases, or execute user code.

These constraints are deliberate and visible. Farfield will not describe a projection, runtime, or security property as production-ready before its recovery and conformance tests exist.

## Repository layout

```text
cmd/farfield/          CLI and local server entrypoint
history/               immutable agent-history domain
runtime/               durable-execution protocol and state machine
server/                HTTP API and embedded inspector
storage/               object-storage contract and implementations
internal/              private shared mechanics
protocol/              language-neutral schemas and fixtures
sdk/                   native SDK homes
docs/                  design and operational documentation
```

## Principles

1. Acknowledged state must survive process loss.
2. Object storage is authoritative; indexes are replaceable.
3. Immutable facts are never silently overwritten.
4. Run identity, conversation identity, and trace identity remain distinct.
5. Existing agents can adopt History without adopting Farfield Runtime.
6. Users can export and inspect their data without Farfield Cloud.

## Contributing

Run `make check` before opening a pull request. See [CONTRIBUTING.md](CONTRIBUTING.md) for the compatibility rules that matter most at this stage.

Licensed under Apache-2.0.
