# Farfield TypeScript SDK

Durable agent history and execution with a typed, Node-native API.

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

## Explore and resume

```ts
const records = await ff.query({ agent: "support-agent", kind: "tool.result", limit: 50 });
const conversations = await ff.conversations(20);
const run = await ff.getRun("run_123");
const events = await ff.runEvents(run.id);
```

The package is ESM-only, requires Node 20+, ships declarations and source maps,
and has no runtime dependencies.
