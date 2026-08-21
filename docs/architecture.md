# Architecture

Farfield has one Go core and multiple native SDK edges.

## Authority

Object storage contains authoritative immutable records, payloads, and portable
corpora. Query indexes, caches, dashboards, labels, cost aggregates, and
evaluation results are derived views unless their protocol explicitly states
otherwise.

History commits any external blobs before the conversation-local segment that
references them. A crash may therefore leave an orphan payload, which
verification reports. It must never expose a committed record pointing to
content that was not durably acknowledged.

## Identity

- A **conversation** groups related interactions across processes, frameworks, traces, and time.
- A **trace** retains external telemetry correlation.
- A **record** is immutable evidence associated with a conversation.

A conversation may span multiple traces, while a trace may describe only one
turn or operation. Farfield preserves both identities instead of forcing one
to stand in for the other.

## Package boundaries

```text
cmd/farfield
    └── internal/cli
          ├── server ──► ingest/otlp ──► history
          ├── history ──► storage
          └── internal/storeopen ──► storage/{s3store,gcsstore}

sdk/{python,typescript,go} ──► versioned HTTP and OTLP protocols

internal/canonicaljson     private persisted-byte encoding
internal/identity          private identifier generation
openapi.yaml               language-neutral HTTP contract
```

Top-level packages represent stable product concepts. Implementation details remain under `internal/`. There is deliberately no `core`, `common`, or `utils` package.

## Compatibility

- Persisted objects carry an explicit schema version.
- Readers reject unsupported major versions.
- Object keys and hash inputs are protocol behavior.
- Golden fixtures are consumed by every SDK.
- Released immutable bytes are never reinterpreted silently.

## Design evolution

Substantial architectural changes are developed in
[design documents](design/README.md). A proposed design describes direction and
tradeoffs; only implemented protocol and operational documentation define
current guarantees.

The [agent workload model](design/0002-agent-workload-storage-fit.md) defines
the burst, idle, branching, replay, artifact, and privacy characteristics that
storage and query designs must validate rather than assuming agent history is
ordinary request tracing.
