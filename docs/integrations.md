# Agent framework integrations

Farfield accepts standard OTLP/HTTP traces and ships direct adapters for the two
major SDKs whose supported extension point is not OTLP. Framework-specific
payloads remain in immutable History; Farfield also normalizes common agent,
model, tool, workflow, retrieval, status, and token fields for filtering and
inspection.

## Support matrix

| Framework or SDK | Integration | Languages | Verified contract |
| --- | --- | --- | --- |
| OpenAI Agents SDK | Direct trace exporter or OpenInference | Python, TypeScript | Real SDK lifecycle test |
| Claude Agent SDK | Direct hooks or OpenInference | Python, TypeScript | Real hook types and matcher test |
| PydanticAI | Native OTel GenAI | Python | Convention fixture |
| AutoGen | Native OTel GenAI | Python | Convention fixture |
| AG2 | OpenInference instrumentation | Python | OpenInference agent fixture |
| Google ADK / agents-cli | Native OTel GenAI | Python | Convention fixture |
| Strands Agents | Native OTel GenAI | Python, TypeScript | Convention fixture |
| Vercel AI SDK | Native OpenTelemetry | TypeScript | Vercel attribute fixture |
| Mastra | `@mastra/otel-exporter` | TypeScript | OTel GenAI fixture |
| Semantic Kernel | Native OpenTelemetry | Python, .NET | OTel GenAI fixture |
| Microsoft Agent Framework | Native OpenTelemetry | Python, .NET | OTel GenAI fixture |
| LangChain / LangGraph | OpenInference or LangSmith OTel | Python, TypeScript | OpenInference and LangSmith fixtures |
| LlamaIndex | OpenInference instrumentation | Python, TypeScript | OpenInference fixture |
| CrewAI | OpenInference/OTel instrumentation | Python | OTel GenAI fixture |
| Agno | OpenInference instrumentation | Python | OpenInference agent fixture |
| DSPy | OpenInference instrumentation | Python | OpenInference model fixture |
| Haystack | OpenInference instrumentation | Python | OpenInference chain fixture |
| Hugging Face smolagents | OpenInference instrumentation | Python | OpenInference agent fixture |
| BeeAI | OpenInference instrumentation | Python, TypeScript | OpenInference agent fixture |
| TanStack AI | OpenInference middleware | TypeScript | OpenInference model fixture |
| AWS Bedrock Agent Runtime | OpenInference instrumentation | TypeScript | OpenInference agent fixture |
| MCP clients and servers | OpenInference instrumentation | Python, TypeScript | OpenInference tool fixture |
| Spring AI / LangChain4j | OpenInference instrumentation | Java | OpenInference model and chain fixtures |
| OpenAI, Anthropic, Bedrock and other model SDKs | OpenInference/OTel instrumentation | Framework-dependent | OTel GenAI/OpenInference fixtures |

“Convention fixture” means Farfield tests the documented wire attributes and
their normalized result. “Real SDK lifecycle test” means Farfield's adapter is
executed through the installed framework's actual exporter or hook types. See
[the integration design decision](design/0007-protocol-first-integrations.md)
for the compatibility policy.

## Send OTLP traces

Point any OTLP/HTTP exporter at the Farfield server:

```bash
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://127.0.0.1:8787/v1/traces
export OTEL_SERVICE_NAME=my-agent
```

The endpoint accepts protobuf and OTLP JSON, supports gzip, returns standard
partial-success responses, and only acknowledges valid spans after their
History segments are durable. A collector can fan the same telemetry out to
Farfield and an existing APM backend.

Content capture is controlled by the producing framework. For example, Vercel
AI SDK offers `recordInputs` and `recordOutputs`; Semantic Kernel and Google ADK
keep sensitive prompt content off by default; PydanticAI exposes instrumentation
settings. Farfield's native SDK `before_send`/`beforeSend` hook can additionally
redact direct-adapter events before transport.

Framework setup follows each project's supported telemetry path:

- [PydanticAI instrumentation](https://ai.pydantic.dev/logfire/)
- [AutoGen tracing](https://microsoft.github.io/autogen/stable/user-guide/agentchat-user-guide/tracing.html)
- [Google agents-cli observability](https://google.github.io/agents-cli/guide/observability/)
- [Strands trace setup](https://strandsagents.com/docs/user-guide/observability-evaluation/traces/)
- [Vercel AI SDK telemetry](https://ai-sdk.dev/docs/ai-sdk-core/telemetry)
- [LangChain/LangGraph OpenTelemetry](https://docs.langchain.com/langsmith/trace-with-opentelemetry)
- [Semantic Kernel telemetry](https://learn.microsoft.com/en-us/semantic-kernel/concepts/enterprise-readiness/observability/telemetry-with-console)

For frameworks without native OTLP export, use their maintained OpenInference
instrumentor and configure its OpenTelemetry exporter with the environment
above. The common Python package names are:

| Workload | Instrumentor package |
| --- | --- |
| Agno | `openinference-instrumentation-agno` |
| AG2 | `openinference-instrumentation-ag2` |
| CrewAI | `openinference-instrumentation-crewai` |
| DSPy | `openinference-instrumentation-dspy` |
| Google ADK | `openinference-instrumentation-google-adk` |
| Haystack | `openinference-instrumentation-haystack` |
| LangChain / LangGraph | `openinference-instrumentation-langchain` |
| LlamaIndex | `openinference-instrumentation-llama-index` |
| smolagents | `openinference-instrumentation-smolagents` |

OpenInference also maintains JavaScript instrumentation for BeeAI, LangChain,
MCP, Bedrock Agent Runtime, OpenAI, Anthropic, TanStack AI, Vercel AI SDK and
related libraries; Java instrumentation for Spring AI and LangChain4j; and Go
instrumentation for the OpenAI and Anthropic SDKs. The current package catalog
and setup examples live in the
[OpenInference repository](https://github.com/Arize-ai/openinference). Farfield
does not republish those instrumentors: applications keep the maintained
instrumentation and change only the OTLP destination.

## OpenAI Agents SDK

Python:

```bash
pip install 'farfield[openai-agents]'
```

```python
from agents.tracing import add_trace_processor
from agents.tracing.processors import BatchTraceProcessor
from farfield import Farfield
from farfield.integrations.openai_agents import FarfieldTracingExporter

exporter = FarfieldTracingExporter(Farfield())
processor = BatchTraceProcessor(exporter, schedule_delay=0.25)
add_trace_processor(processor)

# Run agents normally.

processor.force_flush()
processor.shutdown()
exporter.shutdown()
```

TypeScript:

```ts
import { BatchTraceProcessor, addTraceProcessor } from "@openai/agents";
import { Farfield } from "@farfield/sdk";
import { FarfieldOpenAIAgentsExporter } from "@farfield/sdk/integrations/openai-agents";

const exporter = new FarfieldOpenAIAgentsExporter(new Farfield());
const processor = new BatchTraceProcessor(exporter);
addTraceProcessor(processor);

// Run agents normally.

await processor.forceFlush();
await processor.shutdown();
await exporter.shutdown();
```

The OpenAI `group_id` becomes the Farfield conversation ID. Agent, generation,
function, MCP, handoff, guardrail, task, turn, and voice spans receive stable
Farfield kinds while the complete exported payload is retained.

## Claude Agent SDK

Python:

```bash
pip install 'farfield[claude-agent-sdk]'
```

```python
from claude_agent_sdk import ClaudeAgentOptions, query
from farfield import Farfield
from farfield.integrations.claude_agent_sdk import FarfieldClaudeAgentHooks

capture = FarfieldClaudeAgentHooks(Farfield())
options = ClaudeAgentOptions(hooks=capture.matchers())
try:
    async for message in query(prompt="Inspect this repository", options=options):
        print(message)
finally:
    await capture.shutdown()
```

TypeScript:

```ts
import { query } from "@anthropic-ai/claude-agent-sdk";
import { Farfield } from "@farfield/sdk";
import { FarfieldClaudeAgentHooks } from "@farfield/sdk/integrations/claude-agent-sdk";

const capture = new FarfieldClaudeAgentHooks(new Farfield());
try {
  for await (const message of query({
    prompt: "Inspect this repository",
    options: { hooks: capture.matchers() },
  })) {
    console.log(message);
  }
} finally {
  await capture.shutdown();
}
```

Hook callbacks enqueue locally and do not wait for Farfield I/O. `shutdown()` is
the delivery boundary and must be awaited after the agent finishes. Tool,
permission, prompt, session, subagent, compaction, task, worktree, and other
available lifecycle events are retained under
`farfield.claude_agent_sdk.hook.v1`.

## Delivery semantics

- Direct `Farfield.capture` calls are durable acknowledgments.
- Background processor `submit` means admitted to a bounded in-process queue.
- `flush`/`shutdown` means all events admitted before the call were delivered,
  or returns/reports failure.
- OTLP export acknowledgments are durable Farfield commits.
- Stable trace/span IDs make repeated OTLP and OpenAI exporter batches
  idempotent.
