# 0006: Disposable indexed search

- **Status:** Implemented
- **Date:** 2026-08-20

## Decision

Farfield provides full-text and structured search through an embedded inverted
index. The index is a disposable local projection. Immutable History segments
and blobs in object storage remain the only durable authority.

The first query synchronizes the index from authoritative segment identities.
A checksummed, gzip-compressed local cache avoids re-reading already applied
segments after restart. Missing, corrupt, or stale cache state is repaired from
the object store. Writes through the same process update the index immediately;
external writers are discovered asynchronously so warm searches never wait on
remote storage latency.

## Query contract

- Bare terms are case-insensitive and combined with AND.
- Quoted terms require an adjacent phrase.
- A trailing `*` performs prefix matching; prefixes require two characters and
  expansion is bounded.
- Results use BM25 relevance and deterministic event-time/record-ID tie breaks.
- Conversation, trace, kind, agent, tool, status, tags, and event-time bounds
  are indexed or applied as exact filters.
- Results include the sealed record envelope, score, and a bounded plain-text
  snippet. Authoritative content remains available through the timeline/read
  APIs.

JSON object keys and scalar values are searchable. Tokenization is
Unicode-aware over letters and numbers. The index does not currently apply
language-specific stemming, fuzzy spelling correction, or semantic/vector
similarity.

## Why not another search service

Requiring Elasticsearch, OpenSearch, or a hosted search API would turn an
optional acceleration layer into a second operational and durability
dependency. It would also weaken the promise that a developer can clone
Farfield, point it at a bucket, and search their agent evidence immediately.

General-purpose embedded engines were evaluated. The implemented index keeps
the release as one Go binary, avoids native build requirements, and directly
models agent-specific filter fields and immutable source repair. This is not a
claim that Farfield should build a general-purpose search database.

## Failure and freshness model

The local index never participates in History acknowledgment. A process crash
cannot damage authoritative records. Cache files are replaced atomically and
checksummed; an unreadable cache causes a rebuild.

An initial cold search waits for synchronization. Warm searches are local.
Same-process appends are read-your-writes. External-writer freshness is
eventual and normally bounded by the one-second refresh interval plus object
read time.

## Scale path

The local index is the correct first-release shape, but having every replica
rebuild from every segment is not the final distributed design. Immutable
search packs, term dictionaries, field postings, and generation manifests can
be built in object storage and downloaded or range-read by query replicas.
Those packs remain derived, rebuildable data and do not change the History
protocol.

The reproducible Go benchmark exercises phrase, prefix, metadata, and tag
filtering across 10,000 agent records:

```bash
go test ./history -run '^$' -bench BenchmarkSearchTenThousandAgentRecords -benchmem
```
