# Farfield TypeScript SDK

Durable agent history with a typed, Node-native API.

```bash
npm install @farfield/sdk
```

Start a local Farfield server, then capture and read a conversation:

```ts
import { Farfield } from "@farfield/sdk";

const ff = new Farfield(); // FARFIELD_ENDPOINT and FARFIELD_TOKEN also work

const conversationId = "conv_support_123";
await ff.withConversation(
  { id: conversationId, agent: "support-agent", tags: { env: "dev" } },
  async (conversation) => {
    await conversation.message("user", { text: "Where is my order?" });
    await conversation.message("assistant", { text: "Let me check." });
  },
);

const timeline = await ff.timeline(conversationId);
console.log(timeline.map(({ record, content }) => [record.kind, content]));

const result = await ff.search({ text: '"order shipped" lookup*', agent: "support-agent", tags: { env: "prod" } });
console.log(result.hits.map(({ record, score, snippet }) => [record.id, score, snippet]));
```

Every successful write has been acknowledged by Farfield after its authoritative
object-store commit. IDs and timestamps are generated before the first request,
and retries reuse the exact same body.

## Batch a turn into one durable segment

```ts
const segment = await ff.conversation("conv_123").batch(async (batch) => {
  batch.message("user", "Search for a flight");
  batch.capture({ kind: "tool.call", tool: "flight_search", content: { airport: "SFO" } });
  batch.toolResult("flight_search", { flights: 12 });
});
```

## Redact before data leaves the process

```ts
const ff = new Farfield({
  beforeSend(event) {
    if (event.kind === "debug.internal") return null;
    return { ...event, content: "[redacted]" };
  },
});
```

`withConversation` uses `AsyncLocalStorage.run()`, so scoped IDs and tags follow
promises and callbacks without leaking across concurrent requests.

## Background capture

```ts
import { BackgroundProcessor, Farfield } from "@farfield/sdk";

const processor = new BackgroundProcessor(new Farfield(), {
  maxQueueSize: 8192,
  maxBatchSize: 128,
});
await processor.submit({
  conversationId: "conv_123",
  kind: "model.generation",
  content: { model: "claude" },
});

if (!(await processor.shutdown())) throw new Error("Farfield delivery failed");
```

`submit()` acknowledges bounded queue admission. `flush()` and `shutdown()` are
the delivery boundaries; counters report committed, dropped, failed, pending,
and batch totals.

## Agent frameworks

OpenAI Agents and Claude Agent SDK have typed adapters at
`@farfield/sdk/integrations/openai-agents` and
`@farfield/sdk/integrations/claude-agent-sdk`. Frameworks that emit OTel GenAI
or OpenInference send directly to Farfield's OTLP endpoint. See the complete
[integration guide](../../docs/integrations.md).

## Explore

```ts
const records = await ff.query({ agent: "support-agent", kind: "tool.result", limit: 50 });
const conversations = await ff.conversations(20);
```

The package is ESM-only, requires Node 20+, ships declarations and source maps,
and has no required runtime dependencies. Agent SDK peers are optional.
