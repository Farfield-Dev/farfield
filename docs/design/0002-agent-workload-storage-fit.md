# 0002: Agent history workload and object-storage fit

- Status: Proposed
- Scope: capture, storage layout, querying, search, replay, and artifacts

## Decision

Farfield should optimize object storage for the authoritative history of agent
conversations and traces, not treat the bucket as the synchronous coordinator
for every live operation. Immutable semantic events, trace data, and large
artifacts are an unusually good fit for object storage. Token-by-token UI
streaming, locks, queues, timers, and mutable coordination are different
problems and are outside Farfield's current product contract.

This keeps one durable dependency while allowing disposable local memory and
disk for speed. A successful direct capture or explicit flush means the
authoritative object-store commit completed. Background queue admission does
not make that claim.

## Why agent history is not ordinary request tracing

Agent workloads combine several shapes that general trace systems do not
usually optimize together:

- A conversation can span hours or days and contain many separate traces.
- One turn produces a burst: model input, streamed deltas, tool calls, tool
  results, handoffs, usage, and a completed response.
- The useful debugging unit is often a semantic event or complete turn, not an
  individual transport span.
- Histories branch through retries, alternative attempts, handoffs, and human
  interventions.
- Payloads are heterogeneous: small metadata, large prompts, retrieved
  documents, code patches, audio, images, and files.
- Model and tool content can be more sensitive than ordinary infrastructure
  telemetry, so capture and redaction choices must be explicit.
- Evaluation revisits old traces in bulk and compares versions, datasets, and
  outcomes long after the hot operational window.
- Failures and rare long trajectories are disproportionately valuable even
  when they are a small fraction of total traffic.

The distinction leads to two first-class identities. A **conversation** groups
related interactions across time and frameworks. A **trace** preserves the
telemetry lineage for one operation or turn. Neither should be derived from or
collapsed into the other.

## Workload families

| Workload | Write shape | Important reads | Storage implication |
| --- | --- | --- | --- |
| Interactive chat | Short bursts separated by think time | Recent conversation timeline | Coalesce a completed turn; keep conversation-local layout |
| Tool-using agent | Nested bursts with large or slow results | Causal tool path and failures | Preserve trace/span relationships and semantic tool events |
| Long-horizon agent | Many bursts, idle gaps, and retries | Full attempt history, selected failure windows | Immutable evidence survives process loss; avoid full-corpus scans |
| Multi-agent system | Parallel branches and handoffs | One branch, one agent, or merged conversation | Preserve parent and trace links; index agent metadata |
| Voice/realtime | Very high-rate deltas plus large media | Transcript, interruptions, selected audio ranges | Store semantic milestones separately from optional raw media |
| Evaluation/analytics | Bulk historical scans | Version, cost, outcome, label, and cohort queries | Compact into range-readable packs and derived indexes |

## Where object storage aligns

Object storage is strong for the properties Farfield wants to make durable:

1. **Append-heavy evidence.** Immutable segments avoid update contention and
   make ambiguous retries recoverable through stable IDs and conditional
   creation.
2. **Large artifacts.** Content-addressed blobs make model payloads, audio,
   files, and evaluation artifacts economical and independently verifiable.
3. **Long retention.** History outlives any Farfield process, local disk, or
   proprietary control plane and can use provider lifecycle policies.
4. **Parallel scans.** Evaluation and analytics can read independent packs and
   byte ranges concurrently at high aggregate throughput.
5. **Customer ownership.** BYOC, direct inspection, simple backup, and
   rebuildable product views follow naturally from an open bucket format.
6. **Strong immutable primitives.** S3 conditional creation and GCS generation
   preconditions provide the serialization needed for create-if-absent writes.

## Where object storage fights the workload

| Mismatch | User-visible failure in a naive design | Farfield response |
| --- | --- | --- |
| Tens of milliseconds per small request | Slow capture and waterfall timelines | Batch semantic events; fetch independent objects concurrently |
| Listing a broad prefix | Slow conversation list and global filters | Immutable projection deltas, snapshots, and disposable indexes |
| Object-per-event overhead | High request cost and huge read amplification | Conversation-local multi-event segments |
| No native full-text index | Every search scans every payload | Rebuildable BM25 index now; object-backed index generations later |
| Long chains of dependent writes | Poor fit for chatty coordination | Do not use the bucket as a lock, queue, timer, or live scheduler |
| Cold readers lack cache state | First query can be expensive | Checksummed cache snapshots and bounded incremental discovery |
| Many writers share one key range | Ramp-up and hot-prefix risk | Full hashes, sharding, bounded concurrency, and provider-aware backoff |

Farfield does not add another authoritative database to hide these mismatches.
Conversation summaries are object-backed projections. Full-text search is a
disposable local index rebuilt from authoritative segments. Future shared
indexes and compacted packs should also live in object storage and remain
replaceable.

## Capture model

SDKs should emit stable semantic events rather than durably committing every
stream delta. A typical completed turn can include:

- the user message;
- the model request and completed response;
- tool call and result pairs;
- usage, latency, provider, model, and status metadata;
- errors, cancellations, citations, handoffs, and explicit application events.

Raw stream deltas may be retained when needed, but should normally be buffered
into a bounded segment. This gives developers low overhead without weakening
the meaning of a direct durable acknowledgment. The processor API must expose
queue saturation, delivery failures, `flush`, and `shutdown` so loss is never
silent.

## Read model

The primary interactive paths are:

1. list recent conversations;
2. load one hydrated conversation timeline;
3. search content and filter by conversation, trace, kind, agent, tool, status,
   tag, or time;
4. inspect one record and its original content;
5. export or scan a corpus for evaluation and analytics.

The authoritative layout should be optimized for recovery and these known
dimensions. It does not need to be a general OLTP database. Conversation
prefixes bound timeline discovery; projection snapshots make conversation
lists cheap; the search index provides interactive global queries. Compacted
range-readable packs are the next scale step when segment counts become the
dominant cost.

## Benchmark profiles

Generic object-store throughput benchmarks are necessary but insufficient.
Farfield should publish reproducible profiles that measure end-to-end developer
operations:

- single direct capture and 10/50/200-event segment commits;
- warm and cold recent-conversation lists;
- timelines with 10, 100, 1,000, and 10,000 events, with mixed blob sizes;
- warm and cold full-text search across increasing corpus sizes;
- concurrent writers to one and many conversations;
- cache loss followed by projection and search rebuild;
- provider throttling, ambiguous responses, corruption, and partial writes;
- large artifact upload and ranged retrieval;
- idle periods followed by bursty traffic.

Report p50, p95, p99, bytes transferred, object requests, and cost—not only
aggregate MB/s. Run same-region S3 and GCS profiles with documented instance,
region, client, concurrency, object-size, and cache state.

## Design rules

1. Object storage is the only required durable dependency.
2. Acknowledged direct writes and completed flushes survive loss of every
   Farfield process and local disk.
3. Stable IDs are generated before the first attempt and reused verbatim.
4. Authoritative objects are immutable and checksummed.
5. Small related events are committed in conversation-local segments.
6. Large content is addressed by digest and stored once.
7. Interactive views never require a broad authoritative scan on every read.
8. Every accelerator is disposable and has an explicit rebuild path.
9. Raw provider data is preserved where practical so evolving normalizers do
   not destroy evidence.
10. Privacy hooks execute before data leaves the agent process.
11. Conversation and trace identities remain distinct.
12. Live execution coordination is a non-goal for this product surface.

## Open questions

- What segment size and flush window best balance capture latency, request
  cost, and failure exposure for each SDK?
- When should raw stream deltas be retained by default, sampled, or omitted?
- What shared object-backed index format gives multiple replicas fast cold
  starts without making the index authoritative?
- Which fields belong in the immutable envelope versus a versioned derived
  projection?
- How should encryption interact with content addressing, tenant isolation,
  deduplication, and cryptographic erasure?
- Which public agent corpora are representative enough for reproducible
  conversation, tool, coding, voice, and evaluation benchmarks?

## Evidence and prior art

- [OpenAI Agents SDK tracing](https://openai.github.io/openai-agents-python/tracing/):
  traces, nested spans, conversation grouping, and sensitive payload controls.
- [OpenAI Agents SDK streaming](https://openai.github.io/openai-agents-python/streaming/):
  raw response events versus completed semantic items and handoffs.
- [OpenAI Agents SDK sessions](https://openai.github.io/openai-agents-python/sessions/):
  persistent conversation history, bounded retrieval, storage adapters, and
  context compaction.
- [OpenAI realtime agents guide](https://openai.github.io/openai-agents-python/realtime/guide/):
  long-lived sessions, incremental audio, tool execution, history, and
  interruption semantics.
- [OpenTelemetry GenAI attributes](https://opentelemetry.io/docs/specs/semconv/registry/attributes/gen-ai/)
  and [semantic convention guidance](https://opentelemetry.io/docs/specs/semconv/how-to-write-conventions/):
  streaming metadata and opt-in verbose or sensitive content.
- [LangGraph persistence](https://docs.langchain.com/oss/python/langgraph/persistence):
  threads, history, pending writes, replay, forks, and state growth.
- [METR task-completion time horizons](https://metr.org/time-horizons/):
  autonomous-agent evaluations spanning a wide range of human task durations.
- [Nebius SWE-agent trajectories](https://huggingface.co/datasets/nebius/SWE-agent-trajectories):
  a public coding-agent corpus with trajectory, patch, outcome, and evaluation
  data.
- [LangSmith evaluation concepts](https://docs.langchain.com/langsmith/evaluation-concepts):
  datasets, repeated experiments, traces, thread evaluation, and version
  comparison.
- [Amazon S3 performance guidelines](https://docs.aws.amazon.com/AmazonS3/latest/userguide/optimizing-performance-guidelines.html):
  parallel requests, byte ranges, retries, and colocated compute.
- [Google Cloud Storage request-rate guidelines](https://docs.cloud.google.com/storage/docs/request-rate):
  autoscaling behavior, key distribution, ramp-up, and retries.
- [Amazon S3 consistency](https://docs.aws.amazon.com/console/s3/UsingObjects.html)
  and [Google Cloud Storage consistency](https://docs.cloud.google.com/storage/docs/consistency):
  strong object read-after-write and listing guarantees.

## Validation required

Before accepting this model, Farfield should publish the benchmark harness,
normalization rules, and corpus profiles; compare object-per-event, segmented,
and compacted layouts; and use deterministic fault injection to validate
ambiguous commits, retries, cache loss, and corruption recovery.
