# 0003: Native SDK experience

- Status: Accepted for initial implementation
- Date: 2026-08-19

## Decision

Farfield's HTTP API remains the language-neutral platform boundary. The native
Python, TypeScript, and Go SDKs are hand-designed product surfaces over that
protocol, not generated OpenAPI clients and not alternate implementations of
the persistence protocol.

Every SDK provides:

- a zero-runtime-dependency core HTTP client;
- stable client-generated IDs before the first network attempt;
- automatic retries of transport failures and explicitly retryable HTTP
  responses using the exact same request body;
- durable-by-default capture calls that return only after Farfield acknowledges
  the record or segment;
- explicit batch capture for one-conversation segments;
- filtered History queries, hydrated record reads, conversation summaries, and
  timelines;
- native conversation context propagation;
- a `before_send` hook that can transform or drop content before it is sent;
- typed server and transport errors;
- endpoint, token, timeout, retry, and default metadata configuration;
- a stable SDK user agent for support and compatibility diagnosis.

High-throughput background batching will be a separate opt-in processor. It
must have a bounded queue, explicit overflow behavior, observable drop/error
callbacks, `flush`, and `shutdown`. It must never make an unflushed event look
durable. The direct client and explicit batch call ship first because their
acknowledgment semantics are easier to understand and test.

## Language shape

Python uses a `src/` package layout, `contextvars` for sync/async task-local
conversation context, context managers for conversations and batches, and both
sync and async clients. TypeScript ships ESM with generated declarations,
targets supported Node releases with built-in `fetch`, and uses
`AsyncLocalStorage.run()` for context. Go follows normal explicit-context
conventions with `context.Context`, functional options, and concrete request
and response types.

Names and wire behavior align across languages, but syntax is idiomatic rather
than mechanically identical.

## Why

OpenTelemetry separates immediate and batch processing and requires bounded,
timeout-aware `ForceFlush` and `Shutdown` behavior. Its library guidance also
keeps exporter transport simple and composes queuing/retry outside it. Farfield
adopts those lessons while preserving a stronger distinction between
"accepted into memory" and "durably committed to the customer's object
store."

Conversation and trace context must survive asynchronous execution. Python
documents `contextvars` as natively supported by `asyncio`; Node recommends
`AsyncLocalStorage.run()` over ambient `enterWith()` for scoped context. Go
keeps context explicit.

The SDKs retain Farfield's conversation and trace identities rather than
collapsing them. Agent-specific helpers use OpenTelemetry GenAI vocabulary
where it is stable enough—provider, model, input/output token usage, tool, and
conversation correlation—while retaining arbitrary JSON content and tags.
Sensitive model inputs, outputs, tool arguments, and retrieval queries are not
captured implicitly; adapters must make content capture and redaction policy
visible.

## Packaging

The Python package uses modern `pyproject.toml` metadata and `src/farfield` to
ensure tests exercise the installed package rather than accidentally importing
the repository checkout. The TypeScript package bundles generated declarations
with its JavaScript output and uses package exports. The Go SDK remains inside
the root Go module so it versions with the persisted protocol and server.

## Sources

- [OpenTelemetry tracing SDK](https://opentelemetry.io/docs/specs/otel/trace/sdk/)
- [OpenTelemetry library guidelines](https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/library-guidelines.md)
- [OpenTelemetry event conventions](https://opentelemetry.io/docs/specs/semconv/general/events/)
- [OpenTelemetry GenAI attributes](https://opentelemetry.io/docs/specs/semconv/registry/attributes/gen-ai/)
- [Python context variables](https://docs.python.org/3/library/contextvars.html)
- [Python Packaging: src layout](https://packaging.python.org/en/latest/discussions/src-layout-vs-flat-layout/)
- [Python Packaging: pyproject.toml](https://packaging.python.org/en/latest/guides/writing-pyproject-toml/)
- [Node asynchronous context tracking](https://nodejs.org/api/async_context.html)
- [TypeScript declaration publishing](https://www.typescriptlang.org/docs/handbook/declaration-files/publishing.html)
- [Go module design and publishing](https://go.dev/doc/modules/developing)
- [Stripe Node SDK retry and idempotency behavior](https://github.com/stripe/stripe-node#network-retries)
- [Langfuse SDK event queuing and batching](https://langfuse.com/docs/observability/features/queuing-batching)
