import assert from "node:assert/strict";
import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import { afterEach, beforeEach, test } from "node:test";

import { APIError, DroppedEvent, Farfield, type Event, type Json, type WireEvent } from "../src/index.js";

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

afterEach(async () => closeServer?.());

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

test("history reads and runtime operations cover the complete API", async () => {
  const ff = new Farfield({ endpoint });
  assert.equal(await ff.health(), true);
  assert.equal((await ff.query({ conversationId: "conv_query", tags: { env: "test" }, limit: 10 }))[0]?.id, "rec_query");
  assert.equal((await ff.search({ text: "hello", tags: { env: "test" } })).hits[0]?.record.id, "rec_query");
  assert.deepEqual((await ff.getRecord("rec_query")).content, { text: "hello" });
  assert.equal((await ff.conversations())[0]?.id, "conv_query");
  assert.equal((await ff.timeline("conv_query"))[0]?.record.id, "rec_query");

  const created = await ff.createRun({ id: "run_one", operationId: "op_create" });
  assert.equal(created.run_id, "run_one");
  assert.equal((await ff.getRun("run_one")).status, "running");
  assert.equal((await ff.runEvents("run_one"))[0]?.operation_id, "op_create");
  assert.equal((await ff.transitionRun("run_one", "waiting")).to, "waiting");
  assert.equal((await ff.checkpointRun("run_one", { checkpoint: { step: 1 } })).to, "running");
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
  if (url.pathname === "/v1/runtime/runs" && request.method === "POST") {
    const value = JSON.parse(body) as { id: string; operation_id: string };
    return send(response, 201, runtimeEvent(value.id, value.operation_id, "queued"));
  }
  if (url.pathname === "/v1/runtime/runs/run_one") return send(response, 200, { id: "run_one", status: "running", sequence: 1, attempt: 1 });
  if (url.pathname === "/v1/runtime/runs/run_one/events") return send(response, 200, [runtimeEvent("run_one", "op_create", "queued")]);
  if (url.pathname.endsWith("/transitions")) {
    const value = JSON.parse(body) as { operation_id: string; to: string };
    return send(response, 201, runtimeEvent("run_one", value.operation_id, value.to));
  }
  if (url.pathname.endsWith("/checkpoints")) {
    const value = JSON.parse(body) as { operation_id: string };
    return send(response, 201, runtimeEvent("run_one", value.operation_id, "running"));
  }
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

function runtimeEvent(runId: string, operationId: string, to: string): Record<string, Json> {
  return { id: "evt_one", run_id: runId, operation_id: operationId, sequence: 0, attempt: 0, kind: "created", to };
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
