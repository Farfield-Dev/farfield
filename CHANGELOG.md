# Changelog

All notable changes to Farfield will be documented here. The project follows semantic versioning after `v1.0.0`; pre-1.0 releases may change APIs and persisted schemas only with explicit migration notes.

## Unreleased

### Added

- Native Python, TypeScript, and Go SDKs with durable capture, explicit batch
  segments, conversation context, privacy hooks, typed errors, History reads,
  and Runtime journal access.
- Exact-body retry behavior with stable client-generated IDs and timestamps.
- SDK package, type, lint, and behavioral checks in CI.

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
