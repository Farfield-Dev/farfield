# 0004: Object-storage-native conversation projection

- **Status:** Implemented
- **Date:** 2026-08-20

## Decision

Farfield materializes conversation summaries using immutable objects in the
same bucket as History. It does not require SQLite, Postgres, Redis, or a search
service for the conversation-list experience.

Each authoritative record or segment has exactly one deterministic projection
delta. Readers reduce those deltas over the newest valid immutable snapshot and
keep the result in process memory. Scanning authoritative History is an explicit
recovery operation and never an implicit query behavior.

## Why

An object store is excellent at durable immutable evidence but a naive global
list is an expensive query plan: listing source objects, fetching every object,
decoding every record, and aggregating only to return a few summaries. The
network request count grows with all retained History even when the response is
small.

The desired product behavior is different:

- opening the inspector should cost roughly a snapshot read plus a delta list,
  not one GET per retained source object;
- repeated lists in one process should require no remote I/O inside a short
  freshness window;
- ingestion and recovery should remain object-storage-native;
- the view must be deletable and reconstructable from customer-owned evidence;
- retry and concurrent-writer behavior must be explicit.

## Object protocol

```text
projections/v1/conversations/
  deltas/<source-sha256-prefix>/<source-sha256>.json
  snapshots/<applied-count>-<created-at>-<snapshot-sha256>.json
```

A delta records its authoritative source key and checksum, conversation ID,
record-count contribution, time bounds, agents, and event kinds. Its own body
is checksummed. The object key is the SHA-256 of the source key, making creation
idempotent without exposing raw identities in listings.

A snapshot contains sorted conversation summaries and the exact delta keys it
has reduced. It is checksummed and immutable. Readers try snapshots newest
first, ignore damaged snapshots, and merge every listed delta absent from the
snapshot. Unseen delta bodies are fetched with bounded concurrency.

## Commit and recovery semantics

The source record or segment commits first. The projection delta commits
second. Only then does append return success. If the second write fails, the
source may already be durable and append returns a repairable projection error.
An exact retry observes the immutable source and creates the same deterministic
delta. Different reuse of the source ID remains an idempotency conflict.

A conversation-list request with no snapshots reduces all available deltas and
publishes its first snapshot. If projection data is damaged, the read fails with
an actionable error instead of unexpectedly running a potentially expensive
History scan.

`farfield history projections rebuild` is the deliberate recovery boundary. It
scans authoritative records and segments, marks the deterministic delta key for
every observed source, merges deltas from concurrent writers, and publishes a
new snapshot.

Within one server process, successful appends update the loaded view
immediately. Deltas from other processes are discovered on a one-second refresh
interval. This is bounded view staleness, not weaker durability: direct reads
and verification continue to operate on authoritative objects.

## Tradeoffs and scale path

This design adds one small object creation per source object. Segment ingestion
therefore amortizes projection overhead across many logical records. Snapshot
compaction currently occurs after 256 new deltas and retains exact applied keys.
Cold reads still list the delta prefix, so very large installations will need
sharded manifests and hierarchical immutable delta packs.

Those packs can replace the projection layout without migrating authoritative
History. Adding a separate query store later remains possible for full-text
search or high-cardinality analytics, but it is an accelerator, not a required
durability dependency.

## Rejected alternatives

**Scan History for every list.** Simple but latency and request cost grow with
retention, and the inspector becomes slower while returning the same amount of
metadata.

**Update one mutable conversation head.** This creates hot keys, lost-update
races, conditional-write retry loops, and a second piece of state that can
silently diverge from immutable evidence.

**Require a database-backed index.** It improves arbitrary query latency but
adds deployment, backup, consistency, and recovery obligations before the
product needs those capabilities.

**Rely only on process memory.** Warm reads are fast, but every restart repeats
the full History scan and multi-process writes are not discoverable.
