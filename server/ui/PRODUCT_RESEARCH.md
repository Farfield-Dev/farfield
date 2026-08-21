# Agent observability product research

Research refreshed 2026-08-20. This document records product decisions behind
the inspector so future iterations do not devolve into a collection of generic
dashboard cards.

## Competitive baseline

| Product | Strong workflow patterns | What Farfield should learn |
| --- | --- | --- |
| [LangSmith](https://docs.langchain.com/langsmith/observability) | Trace and run filtering, saved views, trace comparison, dashboards and alerts, automations, feedback and annotation queues | Make finding a problematic run fast; connect traces to evaluation and operational workflows |
| [Langfuse](https://langfuse.com/docs/observability/data-model) | Explicit session/trace/observation model, trace trees, token/cost/latency analytics, environments and metadata | Preserve hierarchy and show LLM-specific attributes semantically instead of as raw JSON |
| [Langfuse Agent Graphs](https://langfuse.com/docs/observability/features/agent-graphs) | Aggregated workflow shape and expanded execution graph modes | Offer both “understand the system” and “debug this exact run” views once span relationships are sufficiently populated |
| [Braintrust](https://www.braintrust.dev/docs/observe) | Searchable production traces, custom columns, topic discovery, dashboards, and a shared data model between logs and experiments | Make production evidence reusable for evaluation rather than creating a disconnected analytics silo |
| [Arize Phoenix](https://arize.com/docs/phoenix/) | OpenTelemetry tracing, sessions, evaluations, datasets/experiments, prompt playground, span replay, annotations and cost tracking | Keep open standards and local operation while connecting observation, diagnosis, experimentation and replay |

The category baseline is no longer “show the trace.” A competitive product must
help an engineer locate a bad execution, understand its structure, isolate the
expensive or failing step, compare it with a known-good run, and turn production
evidence into a reproducible test.

## Farfield advantage

Competitors generally make a telemetry database the center of the product.
Farfield can make durable evidence the center instead:

- authoritative records stay in object storage controlled by the operator;
- immutable segments and record hashes make integrity inspectable, not implied;
- dashboards and indexes are explicitly rebuildable projections;
- the local inspector requires no account and sends trace content nowhere;
- runtime checkpoints and history can eventually meet in one replay workflow.

This is a product distinction, not only a storage implementation detail. The UI
should continually communicate where evidence lives, whether it is sealed, and
which views are authoritative versus projected.

## Delivery sequence

### Implemented in the first redesign

- projection-backed session inventory with agent and type search;
- automatic selection of the information-rich session rather than the newest
  maintenance record;
- status, duration, turn, model-call, tool-call and token summaries derived from
  the selected authoritative timeline;
- signal and all-record timeline modes with streamed assistant chunks collapsed;
- timeline search across semantic event names, kinds, tools, statuses and content;
- pretty renderers for messages, model requests, agent completion usage, tool
  calls and web-search evidence;
- raw JSON, copy, export, storage location, hash integrity, tags and trace IDs;
- authoritative side-by-side session comparison;
- intentional loading, empty, error, responsive and keyboard-focus states.

### Backend-enabled priorities

1. Add a metrics projection for error rate, p50/p95 latency, token and estimated
   cost trends without scanning authoritative history.
2. Add indexed filter dimensions and a shareable query syntax for status, kind,
   agent, tool, tags, time, latency and token thresholds.
3. Populate and project span/parent relationships consistently, then add trace
   tree and aggregated agent graph views.
4. Add immutable annotations and evaluation results linked to records and traces;
   support promoting production evidence into versioned evaluation datasets.
5. Connect history records to runtime checkpoints for safe replay-from-step and
   recovery diagnosis, with explicit side-effect warnings.
6. Add prompt/model/version dimensions and cost tables so comparisons can explain
   regressions rather than merely show deltas.

### Guardrails

- Do not calculate global vanity metrics by reading every timeline from object
  storage in the browser.
- Do not show controls that have no action.
- Do not infer success, cost or quality when the record schema did not capture it.
- Do not let a formatted view hide the exact immutable record and content hash.
- Do not copy the visual language of an existing vendor; copy proven workflows
  and make provenance the unmistakable Farfield signature.
