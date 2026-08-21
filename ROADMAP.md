# Roadmap

Farfield's destination is an open, object-storage-native history and
observability platform for agents. This roadmap separates that ambition from
what the repository can honestly guarantee today.

## Now: prove the durable substrate

- [x] Go module, CLI, storage boundary, and package architecture
- [x] Canonical immutable History records and content-addressed payloads
- [x] Local filesystem, native GCS, and S3-compatible storage implementations
- [x] Verification and deterministic conformance fixtures
- [x] Immutable segmented history log with inline content and external blobs
- [x] Exact conversation-local object layout with concurrent timeline reads
- [ ] Sharded manifests and compacted range-readable packs ([design](docs/design/0001-object-storage-native-history.md))
- [x] Rebuildable object-backed conversation projection with immutable deltas,
  checksummed snapshots, retry repair, and warm in-memory reads
- [x] Embedded BM25 full-text search with phrase/prefix queries, indexed agent
  metadata filters, disposable disk caching, and authoritative rebuild
- [ ] Sharded query projections and disposable range/block cache
- [ ] Agent-shaped object-storage benchmark and recovery harness ([workload model](docs/design/0002-agent-workload-storage-fit.md))
- [x] Versioned HTTP ingestion and local inspector
- [x] OTLP/HTTP protobuf and JSON ingestion with agent-semantic normalization
- [x] Native Python, TypeScript, and Go SDKs for capture and query
- [x] Bounded background processors in Python, TypeScript, and Go
- [x] Direct OpenAI Agents and Claude Agent SDK adapters in Python and TypeScript
- [x] OTel GenAI/OpenInference compatibility for mainstream agent frameworks
- [ ] Release artifacts, Homebrew installation, and signed containers

## Next: agent operations platform

- Object-storage-native shared search indexes and query acceleration
- Cost, latency, token, tool, and reliability analytics
- Custom dashboards and derived metrics
- Evaluation corpora, replay, comparison, and regression gates
- Automatic labeling and clustering
- Configurable PII detection, redaction, retention, and audit policies
- Multi-tenant cloud, BYOC, and customer-managed encryption
- Voice-agent events and large media artifacts
- Policy-driven sampling without losing high-value agent evidence

## Long-term standard

Farfield should become the system where an organization can answer:

- What exactly did this agent observe, decide, and change?
- Which behavior changed between two versions?
- What did it cost, and why?
- Which data crossed a policy boundary?
- Can we rebuild every operational view from data we own?

Items move forward only with a documented storage contract, failure model, compatibility policy, and recovery test suite.

Substantial changes are tracked in [design documents](docs/design/README.md)
before their behavior becomes a protocol guarantee.
