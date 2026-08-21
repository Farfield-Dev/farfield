# 0005: Query-aligned conversation segments

- **Status:** Implemented
- **Date:** 2026-08-20

## Decision

The authoritative History layout uses the conversation as its locality
boundary:

```text
history/v2/conversations/<conversation-sha256>/segments/<segment-sha256>.json
history/v2/blobs/sha256/<content-prefix>/<content-suffix>
```

Every record belongs to an immutable single-conversation segment. The ordinary
single-record API creates a one-entry segment; the batch API creates a
multi-entry segment. Small canonical JSON is inline. Content above the inline
threshold is committed as a content-addressed blob before its segment.

Timeline readers list the exact full-hash conversation prefix, fetch segments
with bounded concurrency, verify every checksum, merge record order, and fetch
any external blobs concurrently.

## Product requirement

Opening a conversation is the primary debugging operation. Its latency must
scale with the selected conversation, not all retained History or all
conversations that happen to share a coarse shard.

The previous physical shape allowed direct lookup of standalone records by ID,
but a timeline had to scan globally keyed records, fetch their payload blobs,
and inspect a coarse conversation shard. Remote request latency accumulated
even for very small conversations.

## Why this layout

- One exact LIST discovers every authoritative source for a conversation.
- Full hashes retain uniform object-name distribution without exposing IDs.
- Inline content removes a second request from the common small-event path.
- Concurrent immutable reads match object storage's aggregate-throughput model.
- Single and batch APIs share one checksum, retry, verification, and recovery
  protocol.
- No mutable conversation head or additional database is required.

## Commit and retry semantics

A writer validates and seals the complete segment before storage. External
blobs commit first, the segment commits second, and its deterministic projection
delta commits last. The segment is the authoritative visibility boundary.

A stable segment ID makes batch retry exact. A stable record ID deterministically
selects the one-entry segment ID for the single-record API. Equivalent retries
return the committed bytes; different reuse is an idempotency conflict.

## Read tradeoffs

Conversation timelines are optimized. Structured and trace filters use the
disposable index defined in design 0006. Authoritative list-all and
record-ID-only lookup still enumerate conversation segments; these secondary
paths should receive explicit rebuildable locator projections rather than
compromising conversation locality.

When one conversation accumulates enough segments that its exact-prefix LIST
or parallel GET fanout becomes material, immutable time-range packs can compact
older segments. A reader can then load packs plus the uncompacted tail without
changing the record or segment protocol.
