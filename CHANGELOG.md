# Changelog

All notable changes to Farfield will be documented here. The project follows
semantic versioning after `v1.0.0`; pre-1.0 releases may make documented
breaking protocol changes.

## Unreleased

### Added

- Native Google Cloud Storage support using Application Default Credentials and
  atomic `ifGenerationMatch=0` creation, plus an opt-in live conformance suite.
- A live Anthropic web-research agent example with retained GCS-backed traces
  and a query helper.
- Real-server Go and TypeScript SDK smoke examples.
- Native Python, TypeScript, and Go SDKs with durable capture, explicit batch
  segments, conversation context, privacy hooks, typed errors, and History
  reads.
- Exact-body retry behavior with stable client-generated IDs and timestamps.
- SDK package, type, lint, and behavioral checks in CI.
- Embedded BM25 full-text search with phrase and prefix syntax, exact metadata
  and tag filters, snippets, automatic repair, HTTP/CLI access, and native SDK
  methods.
- OTLP/HTTP protobuf and JSON trace ingestion with gzip, partial-success
  responses, durable idempotent segments, and OTel GenAI/OpenInference
  normalization.
- Bounded background capture processors for Python, TypeScript, and Go with
  explicit flush/shutdown semantics and delivery statistics.
- Tested OpenAI Agents and Claude Agent SDK adapters for Python and TypeScript,
  plus documented OTLP paths for mainstream agent frameworks.

### Changed

- Removed the experimental durable run journal and its CLI, HTTP, and SDK
  surfaces to focus Farfield's public contract on agent history and traces.

- History now stores every single and batch append as an immutable segment
  under the full conversation hash. Timelines list only the selected
  conversation and fetch segments and external blobs concurrently.
- Removed the standalone record-plus-blob History format and its compatibility
  reader before the first public release.
- Conversation timelines reuse already-verified segment objects, avoiding one
  object-store read per inline record.
- The embedded inspector summarizes and collapses large provider payloads.

## 0.1.0-alpha.1 - 2026-08-19

### Added

- Go-first repository and `farfield` CLI.
- Immutable local and S3-compatible object storage.
- Content-addressed JSON payloads and checksummed History records.
- Checksummed multi-record History segments with inline small content,
  content-addressed large values, and idempotent ambiguous-commit recovery.
- Idempotent record append, direct read, filtered query, conversations, timeline, and verification.
- Batch append through the Go API, HTTP API, and CLI.
- Versioned HTTP ingestion and query API.
- Embedded local conversation inspector.
- Cross-language protocol schema and golden fixture.
- Database-free Runtime journal with immutable sequence-numbered events,
  validated state transitions, explicit attempts, canonical checkpoints,
  idempotent operation recovery, hash-chain verification, HTTP routes, and CLI
  commands.
