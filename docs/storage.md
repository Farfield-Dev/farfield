# Storage contract

Farfield treats object storage as a database primitive, not a filesystem mounted remotely.

## Immutable creation

Every authoritative write uses atomic create-if-absent semantics. Existing identical bytes make the operation idempotent. Existing different bytes return a conflict. A `HEAD` followed by an unconditional `PUT` does not satisfy this contract because concurrent writers could overwrite each other.

Successful writes must also be immediately visible to subsequent `GET` and
`LIST` operations. Amazon S3 and Google Cloud Storage provide strong
read-after-write and list consistency; an S3-compatible endpoint must match
those semantics to preserve correct immutable appends and projections.

## History layout

```text
history/v2/conversations/<conversation-hash>/segments/<segment-hash>.json
history/v2/blobs/sha256/<first-two-hex>/<remaining-hex>
projections/v1/conversations/deltas/<source-hash-prefix>/<source-hash>.json
projections/v1/conversations/snapshots/<generation>-<time>-<hash>.json
```

Payloads use RFC 8785 JSON Canonicalization Scheme (JCS). Every record is stored
inside an immutable, single-conversation segment. Small content is inline;
larger content uses a content-addressed blob. A normal one-record append creates
a one-entry segment, so single and batch ingestion share one protocol. Raw IDs
do not appear in bucket listings.

The append commit order is:

1. Normalize all content and seal every record and the complete segment.
2. Commit any content that exceeds the inline threshold as immutable blobs.
3. Commit the immutable segment containing all record envelopes and inline
   content.
4. Commit the deterministic conversation-summary projection delta.

The segment commit is the durable acknowledgment for the entire batch. A lost
response can be retried with the same segment ID. Equivalent input returns the
committed segment; different input is an idempotency conflict. A crash before
step 3 may leave orphan blobs but cannot expose a partially committed segment.

Segments contain one conversation and live under its full SHA-256 prefix.
Timeline queries therefore issue one exact-prefix LIST and fetch only that
conversation's segments with bounded concurrency. Global and trace-filtered
queries still scan all segments. Compacted range-readable packs remain the
scale path when one conversation accumulates many segment objects.

## Conversation projection

The conversation-list endpoint does not scan every authoritative record on
each request. After a record or segment commits, Farfield writes one immutable
summary delta to the same object store. The delta key is derived from the
authoritative source key, so the same append retry repairs a missing delta and
cannot count a source twice.

When no snapshot exists, Farfield reduces the projection deltas and commits a
checksummed immutable snapshot; an ordinary read never scans authoritative
History. A cold process loads the newest valid snapshot, lists the delta prefix,
and fetches only unseen deltas with bounded concurrency. The running process
serves the materialized view from memory and checks for external writers at
most once per second. This one-second interval bounds cross-process freshness;
writes through the same process update its view immediately.

The authoritative source commits before its delta. If the delta write fails,
the append reports `FH_PROJECTION_WRITE_FAILED`; retrying with the same record
or segment ID commits the deterministic missing delta. Snapshot or delta damage
never changes History evidence. An operator can deliberately reconstruct the
complete view without another database:

```bash
farfield history projections rebuild --store gs://bucket/prefix
```

Missing or damaged projection objects do not silently trigger an authoritative
scan on the read path.

Snapshots currently retain exact applied-delta keys and compact after 256 new
source objects. That is intentionally simple and correct for the first release.
Hierarchical delta packs and sharded snapshot manifests are the scale path for
avoiding an unbounded delta-prefix listing without changing the authoritative
record format.

## Search projection

Full-text search is an embedded disposable projection, not another durable
database. On its first query a process loads its checksummed local cache when
available, lists authoritative History segments, and fetches only sources the
cache has not applied. A missing or corrupt cache is ignored and rebuilt from
the bucket.

Warm queries execute entirely in process against an inverted index with BM25
ranking, term positions, exact metadata postings, and Unicode-aware tokens.
Writes through the same process become searchable immediately. A warm query
never waits for the periodic object-store LIST used to discover external
writers; that refresh happens asynchronously.

The default server cache lives under the operating system's user cache
directory and is keyed by the store description. `--search-cache :memory:`
disables persistence, while `--search-cache PATH` selects an explicit file.
Deleting the cache loses no evidence:

```bash
farfield history search-index rebuild --store gs://bucket/prefix
```

This first implementation intentionally optimizes a single query process. At
large multi-replica scale, immutable object-backed index packs and manifests
will let replicas download a ready index generation instead of independently
rebuilding from every authoritative segment.

## Local filesystem

The local implementation writes and fsyncs a temporary file, atomically links it into place, and fsyncs the containing directory. It is intended for development and single-host use on filesystems that provide normal local link and sync semantics.

## S3-compatible storage

The S3 implementation sends `If-None-Match: *` with `PutObject`. Amazon S3 and compatible endpoints that implement this precondition can satisfy immutable creation. Compatibility is a behavioral claim, not just an API-shape claim; providers should run the storage conformance suite.

## Google Cloud Storage

Use a `gs://bucket/optional-prefix` store URI. The native GCS implementation
uses the official Go client and its standard Application Default Credentials
chain. It does not route writes through the S3 compatibility API.

`PutIfAbsent` applies `DoesNotExist`, which maps to
`ifGenerationMatch=0`. GCS commits the create only when no live object with the
same name exists. A `412 Precondition Failed` is resolved by reading the
existing immutable object: equal bytes are an idempotent retry, while different
bytes are a conflict.

The live suite is opt-in because it creates retained cloud objects:

```bash
FARFIELD_TEST_GCS_URI=gs://my-test-bucket/farfield \
  go test ./storage/gcsstore -run TestGCSIntegration -count=1 -v
```

The suite verifies first creation, same-body retry, conflicting-body rejection,
immediate reads and lists, missing objects, and concurrent-writer exclusion.
See the official [request preconditions](https://docs.cloud.google.com/storage/docs/request-preconditions)
and [consistency](https://docs.cloud.google.com/storage/docs/consistency)
documentation for the provider guarantees behind this adapter.
