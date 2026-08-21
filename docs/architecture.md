# Architecture

Farfield has one Go core and multiple native SDK edges.

## Authority

Object storage contains authoritative immutable records, payloads, run events, checkpoints, and portable corpora. Query indexes, caches, dashboards, labels, cost aggregates, and evaluation results are derived views unless their protocol explicitly states otherwise.

History commits any external blobs before the conversation-local segment that
references them. A crash may therefore leave an orphan payload, which
verification reports. It must never expose a committed record pointing to
content that was not durably acknowledged.

## Identity

- A **conversation** groups related interactions across processes, frameworks, traces, and time.
- A **trace** retains external telemetry correlation.
- A **run** is one durable execution lineage with one or more attempts.
- A **record** is immutable evidence associated with a conversation.

A run can contribute records to a conversation, but a conversation is not a run. History therefore remains independently adoptable.

## Package boundaries

```text
cmd/farfield
    └── internal/cli
          ├── history ──► storage
          └── internal/storeopen ──► storage/s3store

runtime ──► storage        immutable durable journal

internal/canonicaljson     private persisted-byte encoding
internal/identity          private identifier generation
protocol/                  language-neutral contracts
```

Top-level packages represent stable product concepts. Implementation details remain under `internal/`. There is deliberately no `core`, `common`, or `utils` package.

## Runtime boundary

The Go runtime journal coordinates durable run state with immutable,
sequence-numbered events and no mutable database head. The object-store
create-if-absent operation is its serialization point. It currently owns run
creation, validated transitions, attempts, checkpoints, idempotent recovery,
and chain verification.

The journal is not yet a scheduler. Native workers will execute user code and
communicate through a versioned worker protocol; later leases, timers, and
signals build on the journal. No SDK may require access to private Go
representations.

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
storage and runtime designs must validate rather than assuming agent history is
ordinary request tracing.
