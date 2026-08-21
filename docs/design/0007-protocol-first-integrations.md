# 0007: Protocol-first agent framework integrations

- Status: Implemented
- Date: 2026-08-20

## Context

Agent frameworks expose observability through three broad shapes:

1. OpenTelemetry spans using the GenAI semantic conventions.
2. OpenTelemetry spans using OpenInference attributes.
3. A framework-owned exporter or hook interface.

Farfield needs broad framework coverage without coupling its storage protocol to
the release cadence and internal object model of every agent SDK. It also needs
to preserve framework-specific evidence so new projections can be built without
re-ingesting the original run.

The ecosystem has converged enough to make a shared path practical:

- OpenTelemetry defines [GenAI agent, model, tool, retrieval, and workflow
  conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/).
- OpenInference defines an [AI-oriented span-kind and attribute
  specification](https://github.com/Arize-ai/openinference/tree/main/spec).
- AutoGen, PydanticAI, Google ADK, Strands, Vercel AI SDK, Mastra, Semantic
  Kernel, and Microsoft Agent Framework expose OpenTelemetry-compatible
  telemetry in their supported observability paths.
- LangChain, LangGraph, LlamaIndex, CrewAI, Agno, AG2, DSPy, Haystack,
  smolagents, BeeAI, TanStack AI, Spring AI, LangChain4j, MCP, and provider SDKs
  have maintained OpenInference or OpenTelemetry instrumentors.
- OpenAI Agents exposes a trace exporter interface, while Claude Agent SDK
  exposes typed lifecycle hooks.

## Decision

Farfield treats OTLP/HTTP as the primary framework integration protocol.

- `POST /v1/traces` accepts OTLP protobuf and OTLP JSON, including gzip.
- Every valid span becomes an immutable History record. The original resource,
  scope, attributes, events, links, timing, status, and dropped counts remain in
  `farfield.otel.span.v1` content.
- Common OTel GenAI, OpenInference, LangSmith, and Vercel AI SDK fields are
  projected into Farfield conversation, kind, agent, tool, model, provider,
  status, usage, and tag fields.
- OTLP trace and span IDs produce stable record and segment IDs, making exporter
  retries exactly idempotent.
- Partial invalidity uses the OTLP `partial_success` response. A failure to make
  accepted spans durable fails the export.

Framework-specific adapters are reserved for major SDKs whose supported
extension surface carries information that is not available through OTLP:

- OpenAI Agents: `TracingExporter` adapters for Python and TypeScript.
- Claude Agent SDK: hook adapters for Python and TypeScript.

Adapters use structural or type-only coupling where the language permits it.
They preserve the original exported payload in versioned content and run writes
through the same privacy hooks and bounded processors as native capture.

## What “supported” means

Support is stated at one of three levels:

- **Direct**: Farfield ships an adapter tested against the real SDK interface.
- **Native OTLP**: the framework's documented telemetry can export directly to
  Farfield's OTLP endpoint; its convention has a normalization fixture.
- **Instrumented OTLP**: a documented OpenTelemetry/OpenInference instrumentor
  is required; the emitted convention has a normalization fixture.

This terminology prevents an attribute fixture from being described as a full
framework adapter. The public matrix lives in
[`docs/integrations.md`](../integrations.md).

## Consequences

- One hardened ingestion path covers multiple languages and frameworks.
- Framework upgrades that retain semantic conventions do not require a
  Farfield release.
- Standard OTel fan-out remains possible through a collector.
- Framework-specific attributes remain queryable even before Farfield adds a
  first-class projection for them.
- Content capture remains opt-in in frameworks where prompts and responses are
  considered sensitive.
- Semantic conventions are still evolving. Farfield must preserve raw evidence,
  version its normalized content, and extend conformance fixtures as frameworks
  change.
- OTLP traces represent completed spans, not every live streaming token. Native
  capture remains available when an application needs different durability
  boundaries.

## Compatibility sources

- [AutoGen tracing](https://microsoft.github.io/autogen/stable/user-guide/agentchat-user-guide/tracing.html)
- [PydanticAI instrumentation](https://ai.pydantic.dev/logfire/)
- [Google agents-cli observability](https://google.github.io/agents-cli/guide/observability/)
- [Strands traces](https://strandsagents.com/docs/user-guide/observability-evaluation/traces/)
- [Vercel AI SDK telemetry](https://ai-sdk.dev/docs/ai-sdk-core/telemetry)
- [LangSmith OpenTelemetry and OpenInference mapping](https://docs.langchain.com/langsmith/trace-with-opentelemetry)
- [OpenInference framework instrumentors](https://github.com/Arize-ai/openinference)
- [Semantic Kernel observability](https://learn.microsoft.com/en-us/semantic-kernel/concepts/enterprise-readiness/observability/)
- [OpenAI Agents Python tracing](https://openai.github.io/openai-agents-python/tracing/)
- [Claude Agent SDK hooks](https://platform.claude.com/docs/en/agent-sdk/hooks)
