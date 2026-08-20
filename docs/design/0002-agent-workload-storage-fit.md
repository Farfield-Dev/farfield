# 0002: Agent workload and object-storage fit

- Status: Proposed
- Scope: History, execution state, replay, and storage access patterns

## Summary

Agent workloads are not simply larger request traces. They combine interactive
streams, long-lived execution, nested and parallel actors, non-deterministic
model calls, external side effects, large artifacts, and data that may remain
sensitive for its entire lifetime.

Object storage is a strong durable foundation for the immutable parts of this
workload: committed history, checkpoints, artifacts, evaluation corpora, and
rebuildable indexes. It is a poor primitive for token-by-token delivery,
scheduling, leases, or repeated mutation of a single hot object.

Farfield should therefore be object-storage-native, not object-storage-only:

- object storage is the durable source of truth;
- live deltas may pass through memory or a disposable stream;
- semantic events and execution checkpoints are committed in immutable
  segments;
- manifests, indexes, and caches turn object reads into useful query plans;
- coordination mechanisms remain separate from historical evidence.

This document defines the workload model that storage and runtime designs
should be tested against. It complements the
[object-storage-native history design](0001-object-storage-native-history.md).

## Why agents are a distinct workload

### A conversation is larger than a trace

A conventional trace usually describes one bounded operation. Agent systems
also need a stable identity above that operation: a conversation can contain
many turns and traces, survive process restarts, and resume after an idle
period. The OpenAI Agents SDK exposes a `group_id` specifically to associate
multiple traces with one conversation. LangGraph organizes checkpoints from
successive runs into persistent threads. Anthropic Managed Agents similarly
preserves session history across interactions and checkpoints an idle
sandbox.

Farfield must keep conversation, trace, and run identity independent. Folding
them into one identifier would make ordinary tracing easy but make long-lived
history, retries, and execution attempts ambiguous.

### Progress is bursty and can remain idle indefinitely

Agents alternate between dense activity and waiting. A turn may stream many
text deltas, invoke tools, hand off to another agent, and then finish within
seconds. A durable workflow may instead wait for a timer, a user approval, an
external event, or a retry. Temporal defines durable execution without an
imposed time limit; LangGraph interrupts persist state so execution can resume
later; Anthropic sessions move between running and idle while retaining
history.

The storage system must therefore handle both high-frequency bursts and very
long periods with no writes. Duration alone is not a useful partitioning or
retention boundary.

### The execution shape is causal, not always linear

Agent frameworks support handoffs, agents-as-tools, parallel subagents, and
subgraphs. Parallel work produces a partial order: two events may share a
parent without having a meaningful order relative to each other. Retries add
attempt lineages; time travel creates branches from prior checkpoints.

A single global sequence number would introduce a hot coordination point and
invent ordering that the application did not have. Farfield needs reliable
order within a producer or execution attempt plus explicit causal links across
producers.

### Payloads are semantic and heterogeneous

General tracing often emphasizes names, timings, statuses, and low-cardinality
attributes. Useful agent history can include prompts, responses, system
instructions, tool arguments and results, retrieved documents, code patches,
terminal output, screenshots, files, transcripts, and audio.

These values range from a few bytes to large media objects. They are also not
uniformly safe to retain. OpenTelemetry marks verbose or sensitive GenAI
attributes as opt-in. The OpenAI Agents SDK allows generation, tool, transcript,
and raw audio capture to be disabled because those spans can contain sensitive
data.

An agent store needs a small searchable envelope separated from potentially
large or restricted content. Treating every payload as a span attribute is not
a sufficient storage model.

### Replay has more than one meaning

Agent products use the word replay for three different operations:

1. **Playback** reads recorded evidence without executing user code.
2. **Recovery** restores the last durable state and continues the same run.
3. **Forked re-execution** starts a new lineage from an earlier state, possibly
   with changed input, code, prompt, model, or tools.

The distinction matters because model calls and tools are non-deterministic and
may have external side effects. LangGraph notes that steps after a selected
checkpoint execute again, including LLM calls and API requests. Temporal
instead checks replayed workflow commands against recorded event history and
requires side effects to be isolated. Neither mechanism makes an arbitrary
external action exactly once.

Farfield must record attempt identity, causal parentage, side-effect intent and
outcome, and the versioned execution environment. It must never present
playback as proof that re-execution is safe.

### State can grow faster than event count suggests

Naively storing a complete accumulated conversation after every step produces
quadratic growth. LangGraph documents this problem for long-running threads and
offers delta-based channels for append-heavy state.

Farfield should persist deltas at ordinary boundaries and introduce snapshots
at measured intervals. Snapshots are an acceleration and recovery boundary;
the immutable event history remains the audit trail.

### Failure paths can be larger and more valuable than success paths

Agent workload distributions are not uniform. In the public Nebius
SWE-agent dataset of 80,036 software-engineering trajectories, unresolved
attempts averaged 58.4 steps and roughly 15,241 tokens of context, compared
with 31.3 steps and roughly 8,352 tokens for resolved attempts. This is one
agent and benchmark family rather than a universal distribution, but it
illustrates an important inversion: the records developers most need for
debugging may also be the largest and least cache-friendly.

Farfield should not assume that sampling the largest or slowest histories is
safe. Status, attempt, error, and terminal-transition metadata must remain
queryable without reading the full trajectory, and failure-oriented queries
should be first-class benchmark cases.

## Comparison with adjacent workloads

| Property | Request tracing and logs | Agent workload | Storage consequence |
| --- | --- | --- | --- |
| Primary identity | Request or trace | Conversation, run, attempt, trace, agent | Preserve independent identifiers and relationships |
| Lifetime | Usually bounded by one request | Seconds to days, with long idle periods | Do not depend on an open process or connection |
| Topology | Mostly a span tree | Trees, DAGs, handoffs, parallel branches, forks | Store causal links and per-producer order |
| Write shape | A bounded batch of spans or log lines | Streaming bursts, tool loops, checkpoints, sparse resumes | Coalesce transport deltas into semantic events |
| Payload | Mostly metadata and text | Text, structured values, files, images, audio, code, tool output | Separate record envelopes from content-addressed artifacts |
| Correctness | Telemetry can often be sampled or delayed | Some events determine safe recovery | Make durability class and acknowledgment explicit |
| Replay | Inspect recorded spans | Playback, recovery, or forked execution | Model these as separate operations |
| Query | Service, time, status, trace ID | Conversation timeline, causal path, failure, version, cost, behavior | Maintain agent-specific derived indexes |
| Mutation | Append and expire | Append evidence; revise labels, policy, and derived state | Keep authority immutable and views replaceable |
| Sensitivity | Headers and request bodies may be sensitive | Raw user content, secrets, retrieved data, tool I/O, audio | Enforce capture and retention policy before indexing |
| Versioning | Service deployment | Model, prompt, agent, graph, tool, schema, and code versions | Record the full execution provenance |

This table describes tendencies, not universal rules. A large distributed trace
can resemble an agent run, and a simple chatbot turn can resemble an HTTP
request. The design should accommodate both without optimizing the durable
format around the simplest case.

## Workload families

### Interactive text agents

Interactive agents emit fine-grained deltas for user experience, but their
durable meaning usually appears at coarser boundaries: completed message,
completed tool call, handoff, approval, error, or turn. OpenAI's streaming API
exposes both deltas and completed items, and its Agents SDK separately exposes
high-level run-item events.

Object storage fits completed semantic events and conversation retention. It
fights the latency and request cost of an object per token. The live path should
deliver deltas immediately, while the durable path coalesces them and records
the finalized item with enough progress markers to explain interruptions.

### Tool-heavy and coding agents

Coding agents produce many small decisions around much larger artifacts:
commands, output streams, patches, files, test logs, images, and workspace
snapshots. Tasks also extend beyond the short-request regime; METR evaluates
frontier agents on software tasks whose human completion times range from
seconds to many hours.

This is a favorable object-storage workload when metadata remains compact and
large values become artifacts. Immutable blobs provide cheap retention,
deduplication, range reads, and direct export. Object-per-command or
object-per-output-line layouts would still create excessive request
amplification, so small events belong in segments.

### Multi-agent systems

Managers, handoffs, agents-as-tools, and parallel workers create a dynamic
execution graph. Independent branches are naturally writable in parallel and
therefore align with object storage's aggregate throughput. A shared mutable
conversation head does not.

Each producer should write an independently ordered stream. Events should
carry parent event, parent span, run, attempt, and producer identifiers. A
manifest or derived projection can merge those streams for presentation
without placing a global lock on every event.

### Durable and human-in-the-loop agents

These agents require checkpoints, timers, signals, retries, approvals, and
safe resumption. A checkpoint acknowledged as durable must survive the loss of
every Farfield process and local disk. Object storage is well suited to dormant
state and immutable checkpoints but has a higher commit latency than a local
database or in-memory log.

The runtime should commit at semantic state-transition boundaries rather than
after every internal mutation. It also needs leases, fencing, timers, and work
delivery, but those are coordination concerns; storing their durable evidence
in object storage does not make the bucket a scheduler.

### Realtime and voice agents

Realtime agents maintain long-lived connections, consume and emit incremental
audio, execute tools in the background, and support interruptions. What the
user actually heard may differ from what the model generated, so interruption
and playback progress are part of the history.

Object storage is a natural home for finalized audio chunks and transcripts.
It is not the media transport. The live system should use WebSocket, WebRTC, or
another streaming channel, roll media into bounded chunks, and durably record
turn, interruption, transcript, and playback boundaries. Raw media should be
independently governed because it is large and sensitive.

### Context assembly and long-term memory

The complete history is rarely the exact context sent to a model. Agent SDKs
limit retrieved history, compact older turns, summarize prior work, or retrieve
cross-thread memories by semantic similarity. OpenAI's Agents SDK, for example,
supports bounded session retrieval and compaction that clears and rewrites the
session view. LangGraph separates thread checkpoints from a cross-thread store
that can be searched semantically.

This is a mixed workload. Object storage fits the immutable evidence, source
documents, generated summaries, and versioned embedding inputs. It does not by
itself provide low-latency top-k retrieval at the beginning of every model
call. Context windows, summaries, memories, embeddings, and relevance indexes
should be projections with explicit provenance back to source events. A
compaction must not erase or silently reinterpret the authoritative history.

### Evaluation, backfill, and analytics

Evaluation repeatedly runs one or more application versions over a dataset,
then compares outputs, traces, scores, cost, and latency. Production failures
may be promoted into future datasets. This workload is highly parallel,
scan-oriented, and tolerant of immutable snapshots—an unusually strong fit for
object storage.

Fresh interactive search still needs derived indexes. The corpus, experiment
inputs, outputs, provenance, and evaluator results can remain authoritative in
the bucket, allowing indexes and dashboards to be rebuilt or replaced.

## Candidate workload corpora

Synthetic profiles are necessary for fault injection and controlled scaling,
but public trajectories can anchor their event and payload distributions. The
first corpus study should include:

- [Nebius SWE-agent trajectories](https://huggingface.co/datasets/nebius/SWE-agent-trajectories),
  which contains 80,036 coding-agent attempts with step sequences, patches,
  exit status, and evaluation logs;
- [SWE-agent trajectory output](https://github.com/SWE-agent/SWE-agent/blob/main/docs/usage/trajectories.md),
  which documents thought, action, observation, configuration, prediction,
  and log artifacts produced by a coding harness;
- [tau-bench agent trajectories](https://huggingface.co/datasets/AgentSuite/tau-bench-trajectories),
  which provides multi-turn tool-use trajectories across models;
- [WebArena](https://github.com/web-arena-x/webarena), which publishes browser
  tasks and execution trajectories, including human demonstration traces;
- locally generated OpenAI Agents SDK, LangGraph, and Temporal examples to
  exercise parallel branches, interrupts, recovery, and framework-specific
  event semantics not present in static datasets.

Each import must retain source license and provenance, strip hidden reasoning
that is not lawful or intended for redistribution, and avoid checking large
third-party corpora into the Farfield repository. Reproducible import manifests
and aggregate measurements belong in a future benchmark package.

## Durability classes

Farfield should assign durability semantics to data rather than treating every
event identically.

| Class | Examples | Required behavior | Suitable path |
| --- | --- | --- | --- |
| Execution-critical | Checkpoint, side-effect receipt, approval, timer transition | Acknowledgment only after durable commit | Flush an immutable segment and conditionally publish it |
| Historical | Completed message, tool call/result, handoff, model usage | Bounded buffering; no silent loss after durable acknowledgment | Batched immutable segment |
| Live presentation | Token delta, partial transcript, progress tick, audio frame | Low latency; may be coalesced or reconstructed | Memory or disposable stream, then semantic commit |
| Derived | Search index, label, aggregate, dashboard cache | Rebuildable from authoritative data | Object-backed projection, local disk, or external index |

Whether historical telemetry may be dropped under backpressure must be an
explicit caller policy. Execution-critical records must never be silently
downgraded to buffered telemetry.

## Where object storage aligns

Object storage is strongest for the following properties:

- **Immutable evidence.** Append-only events, attempt history, and snapshots
  map naturally to immutable objects.
- **Large heterogeneous artifacts.** Files, images, audio, and tool output can
  be stored independently and read by range.
- **Long and uneven retention.** Inactive conversations consume storage but no
  database working set or resident process.
- **Parallel replay and evaluation.** Independent objects and ranges can be
  scanned by many workers without a central storage node.
- **Portable ownership.** A bucket can remain in the customer's account and
  outlive Farfield compute or a particular index implementation.
- **Recovery and audit.** Strong read-after-write and list consistency on S3
  and GCS make the bucket a viable recovery boundary.
- **Lifecycle policy.** Raw media, compact history, and derived data can have
  different retention and storage-class rules.

AWS recommends parallel requests, byte-range reads, retries, and colocated
compute for high-performance S3 applications. GCS similarly scales requests
across servers and recommends distributed key ranges and gradual traffic
ramp-up. These properties favor segmented, sharded history and large
range-readable packs.

## Where object storage fights the workload

| Tension | Why it matters for agents | Design response |
| --- | --- | --- |
| Per-request latency | A tool loop or live UI cannot wait on a remote object request for every delta | Buffer and coalesce; commit semantic boundaries |
| Small-object request amplification | Agent streams can generate many tiny events | Pack events into short-lived immutable segments |
| Hot mutable heads | Parallel subagents contend if every event updates one conversation object | Shard writers; publish manifests with conditional writes |
| Live tailing | GET and LIST are not a push transport | Use an optional disposable stream and write-through cache |
| Small random reads | Debuggers jump among failures, tool calls, and branches | Store block indexes and fetch relevant ranges concurrently |
| Immediate global search | Buckets do not provide full-text or high-cardinality query indexes | Build replaceable projections from authoritative history |
| Checkpoint latency | Recovery-critical transitions need synchronous durability | Batch within a step, offer explicit durable flush, measure provider profiles |
| Mutable labels and redaction | User annotations and policy actions change after capture | Version derived views; use tombstones, key erasure, and compaction |
| Exactly-once side effects | A crash can occur between an external action and recording its result | Use idempotency keys, intent/receipt records, and explicit ambiguous outcomes |
| Traffic spikes and key hotspots | Evaluations and fleet restarts can create sudden parallel load | Hash-shard keys, bound concurrency, retry with jitter, and pre-warm where needed |

These are architectural constraints, not reasons to abandon object storage.
They define the pieces Farfield must build around it.

## Storage model implied by the workload

### Preserve semantic boundaries

Transport events are not automatically durable records. SDK adapters should
normalize provider-specific streams into stable events such as message
started, message completed, tool requested, tool completed, handoff,
interruption, checkpoint, and usage observed. Raw deltas may be retained when
requested, but should be packed rather than individually addressed.

### Use causal ordering

Every event should carry a stable event ID, producer ID, producer-local
sequence, timestamp, and optional causal parent. Run and attempt identity
provide execution lineage; trace and span identity preserve telemetry
correlation. Presentation order can be derived without claiming a false global
order.

### Separate records from artifacts

Small envelopes should contain queryable identity, timing, type, status,
policy, provenance, and content references. Large values should be immutable
artifacts with their own checksum, media type, size, encryption metadata, and
retention policy. Inline content remains useful below a measured threshold.

### Combine deltas with periodic snapshots

Execution and conversation state should ordinarily be stored as changes plus
periodic snapshots. Snapshot frequency should be driven by recovery time,
history size, and compaction cost. A snapshot must identify the exact event
high-water marks it incorporates.

### Keep live acceleration disposable

An ingestion process may publish deltas to connected clients and cache newly
written segments. A query service may cache manifests, pack footers, and hot
blocks. Losing all of these layers must affect latency, not correctness or
acknowledged durability.

### Treat policy as part of the format

Capture mode, sensitivity classification, tenant, encryption domain,
retention class, and redaction state must be available without decoding raw
content. Derived indexes must not preserve content beyond its governing
policy. Content-addressing must be scoped so that deduplication does not leak
equality across tenants or encryption domains.

## Benchmark profiles

The engine should be tested with agent-shaped workloads, not only uniform
object transfers. The following are synthetic validation profiles, not claims
about population averages. Their values should evolve from observed open
traces and opt-in production measurements.

| Profile | Initial synthetic shape | Capability under test |
| --- | --- | --- |
| Interactive turn | 50–500 deltas over 1–30 seconds, 1–10 completed items, 8–256 KiB durable content | Live latency, coalescing, durable visibility |
| Tool loop | 100–10,000 events over 5–120 minutes, bursty stdout, 10 MiB–10 GiB artifacts | Segment count, artifact throughput, timeline query |
| Parallel agents | 4–128 producers, nested handoffs, concurrent branch completion | Manifest contention, causal merge, write scaling |
| Idle and resume | Checkpoint, no activity for hours or days, then a short burst | Cold recovery time and dependency on cache state |
| Voice session | Continuous input/output chunks, interruptions, transcript updates, tool calls | Media chunking, live/durable boundary, policy overhead |
| Context assembly | 10,000–1,000,000 source events, bounded recent history, top-k semantic retrieval, periodic compaction | Index freshness, source provenance, cold-start latency |
| Evaluation sweep | 1,000–1,000,000 independent examples across multiple versions | Parallel scan, request cost, aggregate throughput |
| Failure recovery | Crash before and after each commit boundary and side-effect receipt | No acknowledged loss, duplicate handling, ambiguous outcomes |

At minimum, benchmarks should report:

- buffered acceptance, durable acknowledgment, and query visibility latency;
- p50, p95, p99, and p99.9 latency rather than averages alone;
- object operations, bytes transferred, and estimated provider cost per
  logical event and conversation;
- cold and warm time to first event and time to full replay;
- write, read, and space amplification before and after compaction;
- recovery point, recovery time, and results after injected process failure;
- throughput and contention as producers and conversations scale;
- effect of encryption, compression, redaction, and artifact deduplication;
- behavior on S3 Standard, GCS, and at least one S3-compatible implementation.

## Design decisions

This workload model establishes the following direction:

1. Object storage remains Farfield's durable authority, but it is not the live
   transport or scheduler.
2. Conversation, trace, run, attempt, and producer remain distinct identities.
3. Semantic events, not raw provider chunks, are the default durable unit.
4. Execution-critical, historical, live, and derived data have explicit and
   different acknowledgment guarantees.
5. Ordering is local and causal across parallel producers; a global sequence is
   not required for ingestion.
6. Playback, recovery, and forked re-execution are separate APIs and product
   concepts.
7. Large content is stored as governed artifacts; queryable envelopes remain
   small.
8. State uses deltas and periodic snapshots rather than full-copy checkpoints
   at every step.
9. Search, labels, dashboards, and aggregates are disposable projections.
10. Model context, summaries, and long-term memory are versioned projections
    with provenance; they do not replace authoritative history.
11. Benchmarks must reproduce agent-shaped burst, idle, branch, media, and
    evaluation behavior.

## Open questions

- Which semantic event vocabulary is small enough to remain stable across
  agent frameworks while preserving provider-specific extensions?
- What durable batching window provides acceptable checkpoint latency on each
  storage profile?
- Should each producer publish its own manifest, or should segments be
  discoverable through a project-level sharded manifest?
- Which state values require every delta versus periodic observation?
- How should artifact encryption and content addressing interact with
  tenant-scoped deduplication and cryptographic erasure?
- What minimum live-stream abstraction can remain optional without fragmenting
  SDK behavior?
- Which metadata belongs in the authoritative envelope versus a derived index?

## Evidence and prior art

- [OpenAI Agents SDK tracing](https://openai.github.io/openai-agents-python/tracing/):
  traces, nested spans, conversation grouping, and sensitive payload controls.
- [OpenAI Agents SDK streaming](https://openai.github.io/openai-agents-python/streaming/):
  raw response events versus completed run-item events, handoffs, and resume
  state.
- [OpenAI Agents SDK sessions](https://openai.github.io/openai-agents-python/sessions/):
  persistent conversation history, bounded retrieval, storage adapters, and
  context compaction.
- [OpenAI realtime agents guide](https://openai.github.io/openai-agents-python/realtime/guide/):
  long-lived sessions, incremental audio, tool execution, history, and
  interruption semantics.
- [OpenAI Agents SDK multi-agent orchestration](https://openai.github.io/openai-agents-python/multi_agent/):
  managers, handoffs, agents-as-tools, and parallel execution.
- [OpenTelemetry GenAI attributes](https://opentelemetry.io/docs/specs/semconv/registry/attributes/gen-ai/)
  and [semantic convention guidance](https://opentelemetry.io/docs/specs/semconv/how-to-write-conventions/):
  streaming metadata and opt-in verbose or sensitive content.
- [LangGraph persistence](https://docs.langchain.com/oss/python/langgraph/persistence):
  thread checkpoints versus cross-thread stores, pending writes, replay,
  forks, full-state growth, and delta channels.
- [LangGraph semantic search](https://docs.langchain.com/langsmith/semantic-search):
  embedding-based retrieval over cross-thread memories and documents.
- [LangGraph Functional API](https://docs.langchain.com/oss/python/langgraph/functional-api):
  persisted task results, deterministic resume, and idempotency guidance.
- [Anthropic Managed Agent sessions](https://platform.claude.com/docs/en/managed-agents/sessions)
  and [event streams](https://platform.claude.com/docs/en/managed-agents/events-and-streaming):
  persistent multi-interaction sessions, state transitions, streaming events,
  idle resume, and sandbox checkpoints.
- [Temporal workflow execution](https://docs.temporal.io/workflow-execution)
  and [event history](https://docs.temporal.io/workflow-execution/event):
  durable execution, append-only histories, replay, side effects, history
  limits, and continue-as-new.
- [METR task-completion time horizons](https://metr.org/time-horizons/):
  evaluation of autonomous agents on software tasks spanning a wide range of
  human completion times.
- [Nebius SWE-agent trajectories](https://huggingface.co/datasets/nebius/SWE-agent-trajectories):
  a public coding-agent corpus with attempt outcome, trajectory, patch, and
  evaluation-log data.
- [LangSmith evaluation concepts](https://docs.langchain.com/langsmith/evaluation-concepts):
  datasets, repeated experiments, traces, thread-level evaluation, and version
  comparison.
- [Amazon S3 performance guidelines](https://docs.aws.amazon.com/AmazonS3/latest/userguide/optimizing-performance-guidelines.html):
  parallel requests, byte ranges, retries, and colocated compute.
- [Google Cloud Storage request-rate guidelines](https://docs.cloud.google.com/storage/docs/request-rate):
  autoscaling behavior, key-range distribution, ramp-up, and retries.
- [Amazon S3 consistency](https://docs.aws.amazon.com/console/s3/UsingObjects.html)
  and [Google Cloud Storage consistency](https://docs.cloud.google.com/storage/docs/consistency):
  strong object read-after-write and listing guarantees.

## Validation required

Before this workload model is accepted, Farfield should:

- measure the candidate public corpora and collect or generate missing text,
  multi-agent, durable, voice, memory, and evaluation profiles;
- publish the normalization rules used to convert each corpus into Farfield
  events;
- run the benchmark profiles against the current object-per-record layout and
  the proposed segmented layout;
- document where observed distributions differ materially from the synthetic
  profiles;
- validate recovery and side-effect ambiguity with deterministic fault
  injection, not only throughput tests;
- revise the design decisions if object-store commit latency cannot meet the
  measured execution checkpoint requirement.
