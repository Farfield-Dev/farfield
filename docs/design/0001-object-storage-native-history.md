# 0001: Object-storage-native history engine

- Status: Proposed; authoritative conversation layout superseded by
  [0005](0005-query-aligned-conversation-segments.md)
- Scope: History ingestion, query, and replay

## Summary

Farfield should retain object storage as the durable source of truth while
moving from an object-per-event layout to immutable, indexed segments. Query
indexes and caches may use memory or local NVMe, but must remain disposable and
rebuildable from the bucket.

This design preserves the operational properties that make object storage
valuable—customer ownership, simple recovery, independent scaling, and BYOC—
without putting one remote request on every small logical read.

## Motivation

Agent history is predominantly append-only and has predictable access paths:

- capture events as an agent runs;
- retrieve one conversation or trace over a time range;
- replay events in order;
- scan larger windows for debugging, evaluation, and analytics;
- retain large model, tool, audio, and file artifacts.

Object storage provides strong durability, high aggregate throughput, and
portable ownership. Its request latency makes a naive object-per-event layout
inefficient, especially when a query lists a broad prefix and downloads records
serially.

Published measurements illustrate the distinction:

- A same-region S3 benchmark using 4–16 MB objects observed roughly 20 ms p90
  time to first byte and approximately 93 MB/s per download stream. Aggregate
  throughput scaled past 1 GB/s on a `c5.4xlarge` instance.
- A 2025 same-region benchmark of 500 KB objects measured GET p50/p99 of
  approximately 26/86 ms and PUT p50/p99 of approximately 70/137 ms.
- S3 Express measurements reported by Turso for 4 KB operations observed
  approximately 3.8 ms average GET and 6.4 ms average PUT, with 4/7 ms p99.
- Google Cloud Storage measurements published with GlassDB reported 100 KB p90
  latency of 63 ms for reads and 105 ms for writes.

These results are not directly comparable: they use different regions,
clients, object sizes, concurrency, and storage classes. They establish a
useful design envelope rather than a provider ranking. Object stores are strong
at parallel transfer and weaker at long sequences of small dependent requests.

## Design constraints

The history engine should satisfy the following constraints:

1. Object storage remains the only required durable dependency.
2. An acknowledged durable write survives loss of all Farfield processes and
   their local disks.
3. Authoritative objects are immutable, or updated only through a versioned
   conditional operation with an explicit concurrency protocol.
4. Indexes, projections, and caches can be deleted and reconstructed.
5. A cold reader can discover and interpret the format using the bucket and
   published protocol alone.
6. The portable baseline works on conforming S3-compatible storage. Native S3
   and GCS adapters may expose optional performance capabilities.
7. Conversation, trace, and run identity remain independent.

Building a general-purpose transactional database is not a goal. The design
should exploit the append-heavy workload and known query dimensions of agent
history.

## Proposed design

The implemented first-release layout keeps each immutable segment within one
full-hash conversation prefix, as specified in design 0005. The manifest,
range-pack, and compaction ideas below remain forward-looking scale work.

### Immutable write segments

Ingestion groups logical events into immutable segments. A segment is flushed
when a short time window expires, it reaches a target size, or a caller requests
a durable checkpoint. Events from multiple conversations may share a segment
when they map to the same storage shard.

Ordinary event payloads are stored inline. Large or independently reusable
artifacts remain content-addressed blobs. This avoids the two-object write and
read amplification of storing every event body separately while preserving
deduplication for expensive artifacts.

The API should distinguish buffered acceptance from durable acknowledgment.
Tracing can favor low-overhead buffering; execution checkpoints can wait until
the containing segment has been committed to object storage.

### Sharded manifests

Each project is divided into deterministic shards derived from conversation
identity. A small manifest per shard references recent write segments,
compacted packs, schema versions, and high-water marks.

Data and manifest generations are immutable. A small head object is advanced
with compare-and-swap. Amazon S3 supports `If-Match` and `If-None-Match`
conditional writes; Google Cloud Storage provides equivalent generation
preconditions. A writer that loses a manifest race reloads and retries rather
than overwriting concurrent work.

Normal queries consult manifests instead of listing the complete record
namespace. Strongly consistent bucket listing remains a recovery and repair
primitive, not the primary query plan.

### Range-readable packs

Segments are compacted into larger packs. A pack contains compressed blocks,
an index, checksums, and a fixed-size footer. Its index records enough summary
information to eliminate irrelevant blocks before reading their contents:

- minimum and maximum event time;
- conversation and trace membership;
- event kinds, agents, tools, and statuses;
- record counts and block byte ranges;
- optional Bloom filters and column statistics.

A cold query first obtains the small manifest and pack metadata, then fetches
only relevant byte ranges. Independent reads are issued concurrently. Replay
uses sequential prefetch to take advantage of object storage throughput.

### Compaction and derived indexes

Disposable compactors merge recent segments into query-oriented packs, bound
read amplification, apply tombstones, and publish new manifest generations.
Unpublished output is unreachable and can be garbage-collected after a safety
window.

Search, cost aggregates, labels, and evaluation indexes are derived products.
They may use specialized formats, but their durable state must either be stored
in the bucket or be reconstructible from authoritative history.

### Disposable acceleration

Query and ingestion processes may maintain:

- manifest and footer caches in memory;
- decoded block caches in memory;
- compressed range caches on local NVMe;
- write-through entries for newly ingested segments;
- cache-affine routing by project or conversation;
- prefetch and hedged reads for latency-sensitive requests.

None of these layers participate in durable recovery. Losing every cache can
degrade latency but cannot lose acknowledged data.

### Storage profiles

The immutable-segment format is the portable baseline. Provider-specific
profiles may reduce latency without changing the logical history protocol:

- S3 Express One Zone supports single-digit-millisecond access and appendable
  objects in directory buckets.
- Google Cloud Storage Rapid supports zonal appendable objects, concurrent
  readers, and a stateful streaming protocol.

These facilities are suitable for a hot write-ahead log or live segment. Data
can later be finalized or compacted into the same portable pack format used on
standard object storage.

## Consequences and tradeoffs

The design amortizes request latency and cost, removes broad bucket scans from
normal queries, and gives replay a throughput-friendly layout. It also permits
stateless compute and straightforward recovery from customer-owned data.

It does not remove the latency of the first durable object-store commit or a
genuinely cold read. Batching introduces a bounded visibility delay for
buffered telemetry. Manifests require careful fencing, crash recovery, and
garbage-collection tests. Compaction creates temporary write amplification.
Provider-specific append modes require separate conformance coverage.

## Alternatives considered

### One object per event

This is simple and remains useful as a reference format, but it creates request
amplification and poor query behavior at scale.

### Durable metadata in PostgreSQL or DynamoDB

An external metadata database can reduce coordination latency, but it becomes
another source of truth that must be provisioned, recovered, and reconciled.
Farfield may support optional derived projections in such systems, but the base
engine should not require them for correctness.

### A general-purpose object-store LSM engine

An embedded engine such as SlateDB demonstrates that the full design is
possible. Farfield's append-heavy history model and fixed query dimensions
allow a smaller protocol with fewer transactional requirements.

### Querying raw JSON with a general analytics engine

This remains valuable for export and offline analysis, but does not provide the
latency or product semantics expected from interactive conversation debugging.

## Prior art and references

- [turbopuffer architecture](https://turbopuffer.com/docs/architecture):
  object-storage WAL, batching, indexing, and memory/NVMe caching.
- [turbopuffer storage engine](https://turbopuffer.com/blog/zero-cost): LSM
  compaction, per-file metadata, and byte-range reads.
- [WarpStream architecture](https://docs.warpstream.com/warpstream/overview/architecture):
  cross-partition batching and background compaction over object storage.
- [Quickwit architecture](https://quickwit.io/docs/main-branch/overview/architecture):
  immutable splits, hotcache metadata, pruning, and cache-aware search.
- [SlateDB design](https://slatedb.io/docs/design/overview/): WAL, manifests,
  SSTables, and compaction stored in object storage.
- [Apache Iceberg performance](https://iceberg.apache.org/docs/latest/performance/):
  hierarchical manifests and statistics-based file pruning.
- [Lance file format](https://lance.org/format/file/): cloud-oriented pages,
  metadata, footers, and selective reads.
- [Amazon S3 conditional writes](https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html).
- [Amazon S3 Express appendable objects](https://docs.aws.amazon.com/AmazonS3/latest/userguide/directory-buckets-objects-append.html).
- [Google Cloud Storage request preconditions](https://docs.cloud.google.com/storage/docs/request-preconditions).
- [Google Cloud Storage Rapid](https://docs.cloud.google.com/storage/docs/rapid/rapid-bucket).
- [S3 throughput benchmark](https://github.com/dvassallo/s3-benchmark).
- [S3 small-object latency benchmark](https://topicpartition.io/misc/AWS-S3-PUT-latency-benchmark).
- [Turso on S3 Express One Zone](https://aws.amazon.com/blogs/storage/how-turso-built-a-transactional-database-using-amazon-s3-express-one-zone/).
- [GlassDB transactional object-storage experiments](https://blog.mbrt.dev/posts/transactional-object-storage/).

## Validation required

Before this design is accepted, a prototype and benchmark should measure:

- end-to-end buffered and durable append latency;
- segment size and batching-window tradeoffs;
- cold and warm conversation queries at 10, 100, 1,000, and 10,000 events;
- replay throughput and memory use;
- request amplification and estimated operation cost;
- manifest contention and writer failover;
- compaction recovery and garbage-collection safety;
- S3 Standard, at least one conforming S3-compatible implementation, and GCS;
- p50, p95, p99, and p99.9 behavior from colocated and remote compute.
