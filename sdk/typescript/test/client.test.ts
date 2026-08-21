import assert from "node:assert/strict";
import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import { afterEach, beforeEach, test } from "node:test";
import {
  BatchTraceProcessor,
  setTraceProcessors,
  withAgentSpan,
  withFunctionSpan,
  withGenerationSpan,
  withTrace,
} from "@openai/agents";
import type {
  HookCallbackMatcher,
  PostToolUseHookInput,
  PreToolUseHookInput,
  UserPromptSubmitHookInput,
} from "@anthropic-ai/claude-agent-sdk";

import {
  APIError,
  BackgroundProcessor,
  DroppedEvent,
  Farfield,
  FarfieldClaudeAgentHooks,
  FarfieldOpenAIAgentsExporter,
  type Event,
  type Json,
  type WireEvent,
} from "../src/index.js";

interface Received {
  method: string;
  path: string;
  body: string;
  authorization?: string;
}

let endpoint = "";
let closeServer: (() => Promise<void>) | undefined;
let requests: Received[] = [];
let recordFailures = 0;

beforeEach(async () => {
  requests = [];
  recordFailures = 0;
  const server = createServer(async (request, response) => handle(request, response));
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("expected TCP address");
  endpoint = `http://127.0.0.1:${address.port}`;
  closeServer = () => new Promise((resolve, reject) => server.close((error) => (error ? reject(error) : resolve())));
});

afterEach(async () => {
  setTraceProcessors([]);
  await closeServer?.();
});

test("capture scopes metadata and retries the identical body", async () => {
  recordFailures = 1;
  const ff = new Farfield({
    endpoint,
    token: "secret",
    retryDelayMs: 0,
    defaults: { agent: "planner", tags: { env: "test" } },
  });

  const record = await ff.withConversation(
    { id: "conv_one", traceId: "trace_one", tags: { tenant: "acme" } },
    () => ff.capture({ kind: "message.user", content: { text: "hello" } }),
  );

  assert.match(record.id, /^rec_/);
  assert.equal(requests.length, 2);
  assert.equal(requests[0]?.body, requests[1]?.body);
  assert.equal(requests[0]?.authorization, "Bearer secret");
  const sent = JSON.parse(requests[0]!.body) as WireEvent;
  assert.equal(sent.conversation_id, "conv_one");
  assert.equal(sent.trace_id, "trace_one");
  assert.equal(sent.agent, "planner");
  assert.deepEqual(sent.tags, { env: "test", tenant: "acme" });
});

test("async conversation scopes are isolated", async () => {
  const ff = new Farfield({ endpoint });
  await Promise.all(
    ["conv_a", "conv_b"].map((conversationId) =>
      ff.withConversation(conversationId, async () => {
        await new Promise((resolve) => setImmediate(resolve));
        await ff.capture({ kind: "message.user", content: conversationId });
      }),
    ),
  );

  const ids = requests.map((request) => (JSON.parse(request.body) as WireEvent).conversation_id).sort();
  assert.deepEqual(ids, ["conv_a", "conv_b"]);
});

test("conversation batches form one durable segment", async () => {
  const ff = new Farfield({ endpoint });
  const segment = await ff.conversation("conv_batch").batch(async (batch) => {
    batch.message("user", "hello");
    await new Promise((resolve) => setImmediate(resolve));
    batch.toolResult("weather", { sunny: true });
  }, "seg_one");

  assert.equal(segment.id, "seg_one");
  assert.equal(segment.entries.length, 2);
  const sent = JSON.parse(requests[0]!.body) as { records: WireEvent[] };
  assert.equal(sent.records[0]?.conversation_id, "conv_batch");
  assert.equal(sent.records[1]?.tool, "weather");
});

test("beforeSend can redact or drop before transport", async () => {
  const ff = new Farfield({
    endpoint,
    beforeSend(event) {
      if (event.kind === "debug") return null;
      return { ...event, content: "[redacted]" };
    },
  });

  await ff.capture({ conversationId: "conv_one", kind: "message.user", content: "secret" });
  await assert.rejects(
    ff.capture({ conversationId: "conv_one", kind: "debug", content: "noise" }),
    DroppedEvent,
  );
  assert.equal((JSON.parse(requests[0]!.body) as WireEvent).content, "[redacted]");
  assert.equal(requests.length, 1);
});

test("invalid JSON is rejected before transport", async () => {
  const ff = new Farfield({ endpoint });
  await assert.rejects(
    ff.capture({ conversationId: "conv_one", kind: "metric", content: Number.NaN as unknown as Json }),
    TypeError,
  );
  assert.equal(requests.length, 0);
});

test("background processor snapshots scope, batches by conversation, and flushes", async () => {
  const ff = new Farfield({ endpoint, defaults: { tags: { env: "test" } } });
  const processor = new BackgroundProcessor(ff, { maxBatchSize: 10, scheduleDelayMs: 5 });
  await ff.withConversation({ id: "conv_one", agent: "researcher" }, async () => {
    assert.equal(await processor.submit({ kind: "message.user", content: "hello" }), true);
    assert.equal(await processor.submit({ kind: "message.assistant", content: "hi" }), true);
  });
  assert.equal(
    await processor.submit({ conversationId: "conv_two", kind: "tool.result", tool: "search", content: { ok: true } }),
    true,
  );
  assert.equal(await processor.flush(), true);
  assert.equal(await processor.shutdown(), true);

  const segments = requests
    .filter((request) => request.path === "/v1/history/segments")
    .map((request) => JSON.parse(request.body) as { records: WireEvent[] });
  assert.equal(segments.length, 2);
  const groups = new Map(segments.map((segment) => [segment.records[0]!.conversation_id, segment.records]));
  assert.equal(groups.get("conv_one")?.length, 2);
  assert.equal(groups.get("conv_one")?.[0]?.agent, "researcher");
  assert.deepEqual(groups.get("conv_one")?.[0]?.tags, { env: "test" });
  assert.deepEqual(processor.stats(), {
    enqueued: 3,
    committed: 3,
    dropped: 0,
    failed: 0,
    batches: 2,
    pending: 0,
  });
});

test("background processor reports delivery failures", async () => {
  const errors: unknown[] = [];
  const ff = new Farfield({ endpoint: "http://127.0.0.1:1", maxRetries: 0, timeoutMs: 50 });
  const processor = new BackgroundProcessor(ff, {
    scheduleDelayMs: 0,
    onError(error) {
      errors.push(error);
    },
  });
  assert.equal(await processor.submit({ conversationId: "conv_fail", kind: "message.user", content: "hello" }), true);
  assert.equal(await processor.flush(), false);
  assert.equal(await processor.shutdown(), false);
  assert.equal(processor.stats().failed, 1);
  assert.equal(errors.length, 1);
});

test("OpenAI Agents SDK trace lifecycle is captured through its real exporter API", async () => {
  const exporter = new FarfieldOpenAIAgentsExporter(new Farfield({ endpoint }), { defaultAgent: "demo" });
  const processor = new BatchTraceProcessor(exporter, { scheduleDelay: 60_000 });
  setTraceProcessors([processor]);

  await withTrace(
    "research workflow",
    async () =>
      withAgentSpan(
        async () => {
          await withGenerationSpan(async () => undefined, {
            spanId: "span_1123456789abcdef0123456789abcdef",
            data: {
              input: [{ role: "user", content: "Find an answer" }],
              output: [{ role: "assistant", content: "I found it" }],
              model: "gpt-test",
              usage: { input_tokens: 4, output_tokens: 3 },
            },
          });
          await withFunctionSpan(async () => undefined, {
            spanId: "span_2123456789abcdef0123456789abcdef",
            data: { name: "web_search", input: '{"q":"Farfield"}', output: '{"results":1}' },
          });
        },
        { spanId: "span_0123456789abcdef0123456789abcdef", data: { name: "researcher" } },
      ),
    {
      traceId: "trace_0123456789abcdef0123456789abcdef",
      groupId: "conversation_openai_agents",
      metadata: { tenant: "test" },
    },
  );
  await processor.forceFlush();
  await processor.shutdown();
  await exporter.shutdown();

  const segments = requests
    .filter((request) => request.path === "/v1/history/segments")
    .map((request) => JSON.parse(request.body) as { records: WireEvent[] });
  const records = segments.flatMap((segment) => segment.records);
  assert.equal(records.length, 4);
  assert.deepEqual(new Set(records.map((record) => record.conversation_id)), new Set(["conversation_openai_agents"]));
  assert.deepEqual(
    new Set(records.map((record) => record.kind)),
    new Set(["agent.trace", "agent.invoke", "model.generation", "tool.execution"]),
  );
  assert.equal(records.find((record) => record.kind === "tool.execution")?.tool, "web_search");
  assert.deepEqual(exporter.stats(), {
    traces: 1,
    spans: 3,
    bufferedSpans: 0,
    cachedTraces: 0,
    failedExports: 0,
  });
});

test("Claude Agent SDK hook lifecycle is captured through its real callback types", async () => {
  const integration = new FarfieldClaudeAgentHooks(new Farfield({ endpoint }), {
    defaultAgent: "claude-code",
    scheduleDelayMs: 5,
  });
  const matchers = integration.matchers({ timeout: 2 });
  const preMatcher: HookCallbackMatcher = matchers.PreToolUse![0]!;
  const context = { signal: new AbortController().signal };
  const common = {
    session_id: "session_claude_agent",
    transcript_path: "/tmp/transcript.jsonl",
    cwd: "/workspace",
  };
  const prompt = {
    ...common,
    hook_event_name: "UserPromptSubmit",
    prompt: "Inspect the repo",
  } satisfies UserPromptSubmitHookInput;
  const pre = {
    ...common,
    hook_event_name: "PreToolUse",
    tool_name: "Read",
    tool_input: { file_path: "README.md" },
    tool_use_id: "toolu_01",
  } satisfies PreToolUseHookInput;
  const post = {
    ...common,
    hook_event_name: "PostToolUse",
    tool_name: "Read",
    tool_input: { file_path: "README.md" },
    tool_response: { content: "Farfield" },
    tool_use_id: "toolu_01",
  } satisfies PostToolUseHookInput;

  await matchers.UserPromptSubmit![0]!.hooks[0]!(prompt, undefined, context);
  await preMatcher.hooks[0]!(pre, "toolu_01", context);
  await matchers.PostToolUse![0]!.hooks[0]!(post, "toolu_01", context);
  assert.equal(await integration.shutdown(), true);

  const records = requests
    .filter((request) => request.path === "/v1/history/segments")
    .flatMap((request) => (JSON.parse(request.body) as { records: WireEvent[] }).records);
  assert.equal(records.length, 3);
  assert.deepEqual(new Set(records.map((record) => record.conversation_id)), new Set(["session_claude_agent"]));
  assert.deepEqual(new Set(records.map((record) => record.kind)), new Set(["message.user", "tool.call", "tool.result"]));
  const toolResult = records.find((record) => record.kind === "tool.result");
  assert.equal(toolResult?.tool, "Read");
  assert.equal((toolResult?.content as { schema: string }).schema, "farfield.claude_agent_sdk.hook.v1");
  assert.equal(integration.stats().committed, 3);
});

test("history reads cover the complete API", async () => {
  const ff = new Farfield({ endpoint });
  assert.equal(await ff.health(), true);
  assert.equal((await ff.query({ conversationId: "conv_query", tags: { env: "test" }, limit: 10 }))[0]?.id, "rec_query");
  assert.equal((await ff.search({ text: "hello", tags: { env: "test" } })).hits[0]?.record.id, "rec_query");
  assert.deepEqual((await ff.getRecord("rec_query")).content, { text: "hello" });
  assert.equal((await ff.conversations())[0]?.id, "conv_query");
  assert.equal((await ff.timeline("conv_query"))[0]?.record.id, "rec_query");

});

test("server failures are typed and actionable", async () => {
  const ff = new Farfield({ endpoint, maxRetries: 0 });
  await assert.rejects(
    ff.capture({ conversationId: "error", kind: "message.user", content: null }),
    (error) => error instanceof APIError && error.statusCode === 409 && error.code === "FH_CONFLICT",
  );
});

async function handle(request: IncomingMessage, response: ServerResponse): Promise<void> {
  const body = await readBody(request);
  requests.push({
    method: request.method ?? "",
    path: request.url ?? "",
    body,
    ...(request.headers.authorization ? { authorization: request.headers.authorization } : {}),
  });
  const url = new URL(request.url ?? "/", "http://localhost");
  if (request.method === "POST" && url.pathname === "/v1/history/records") {
    const value = JSON.parse(body) as WireEvent;
    if (value.conversation_id === "error") return sendError(response, 409, "FH_CONFLICT", "already exists");
    if (recordFailures > 0) {
      recordFailures -= 1;
      return sendError(response, 503, "FH_BUSY", "try again", true);
    }
    return send(response, 201, record(value));
  }
  if (request.method === "POST" && url.pathname === "/v1/history/segments") {
    const value = JSON.parse(body) as { id: string; records: WireEvent[] };
    return send(response, 201, {
      schema_version: "farfield.history.segment.v1",
      id: value.id,
      conversation_id: value.records[0]?.conversation_id,
      entries: value.records.map((event) => ({ record: record(event), content: event.content })),
      segment_sha256: "b".repeat(64),
    });
  }
  if (url.pathname === "/v1/health") return send(response, 200, { ok: true, service: "farfield" });
  if (url.pathname === "/v1/history/records" && request.method === "GET") {
    assert.equal(url.searchParams.get("tag"), "env=test");
    return send(response, 200, [record({ id: "rec_query", conversation_id: "conv_query", kind: "message.user", content: null } as WireEvent)]);
  }
  if (url.pathname === "/v1/history/search") {
    assert.equal(url.searchParams.get("q"), "hello");
    assert.equal(url.searchParams.get("tag"), "env=test");
    return send(response, 200, { hits: [{ record: record({ id: "rec_query", conversation_id: "conv_query", kind: "message.user", content: null } as WireEvent), score: 1, snippet: "hello" }], total: 1, took_ms: 0.2, indexed_records: 1, index_updated_at: "2026-01-01T00:00:00Z" });
  }
  if (url.pathname === "/v1/history/records/rec_query") return send(response, 200, { record: record({ id: "rec_query", conversation_id: "conv_query", kind: "message.user", content: null } as WireEvent), content: { text: "hello" } });
  if (url.pathname === "/v1/history/conversations") return send(response, 200, [{ id: "conv_query", record_count: 1, first_seen_at: "2026-01-01T00:00:00Z", last_seen_at: "2026-01-01T00:00:00Z", agents: [], kinds: ["message.user"] }]);
  if (url.pathname === "/v1/history/conversations/conv_query/timeline") return send(response, 200, [{ record: record({ id: "rec_query", conversation_id: "conv_query", kind: "message.user", content: null } as WireEvent), content: { text: "hello" } }]);
  sendError(response, 404, "FH_NOT_FOUND", "not found");
}

function record(event: WireEvent): Record<string, unknown> {
  return {
    ...event,
    schema_version: "farfield.history.record.v2",
    occurred_at: "2026-01-01T00:00:00Z",
    recorded_at: "2026-01-01T00:00:00Z",
    record_sha256: "a".repeat(64),
    tags: {},
  };
}

function readBody(request: IncomingMessage): Promise<string> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    request.on("data", (chunk: Buffer) => chunks.push(chunk));
    request.on("end", () => resolve(Buffer.concat(chunks).toString()));
    request.on("error", reject);
  });
}

function send(response: ServerResponse, status: number, value: unknown): void {
  const body = JSON.stringify(value);
  response.writeHead(status, { "Content-Type": "application/json", "Content-Length": Buffer.byteLength(body) });
  response.end(body);
}

function sendError(response: ServerResponse, status: number, code: string, message: string, retryable = false): void {
  send(response, status, { error: { code, message, retryable } });
}
