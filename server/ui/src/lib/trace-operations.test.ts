import { describe, expect, it } from "vitest";
import type { HistoryRecord, TimelineEntry } from "./history";
import { buildTraceOperations, operationsForDensity, rawTraceOperations } from "./trace-operations";

function entry(id: string, kind: string, seconds: number, tool: string | null = null, content: TimelineEntry["content"] = {}): TimelineEntry {
  const record: HistoryRecord = {
    schema_version: "2",
    id,
    conversation_id: "conv_test",
    kind,
    occurred_at: new Date(Date.UTC(2026, 7, 20, 12, 0, seconds)).toISOString(),
    recorded_at: new Date(Date.UTC(2026, 7, 20, 12, 0, seconds, 10)).toISOString(),
    sequence: seconds,
    trace_id: "trace_test",
    span_id: `span_${id}`,
    parent_id: null,
    agent: "test-agent",
    tool,
    status: kind === "tool.result" ? "complete" : null,
    tags: {},
    content: { sha256: id.padEnd(64, "0"), size: 20, media_type: "application/json", key: id },
  };
  return { record, content };
}

describe("trace operations", () => {
  it("pairs a tool call with its result and calculates duration", () => {
    const operations = buildTraceOperations([
      entry("call", "tool.call", 1, "query_logs", { input: { query: "checkout" } }),
      entry("result", "tool.result", 4, "query_logs", { rows: 12 }),
    ]);

    expect(operations).toHaveLength(1);
    expect(operations[0].title).toBe("Query Logs");
    expect(operations[0].entries).toHaveLength(2);
    expect(operations[0].duration).toBe(3_000);
  });

  it("coalesces adjacent assistant chunks without losing the raw view", () => {
    const entries = [
      entry("one", "message.assistant", 1, null, { text: "Hello " }),
      entry("two", "message.assistant", 2, null, { text: "world" }),
    ];
    const operations = buildTraceOperations(entries);

    expect(operations).toHaveLength(1);
    expect(operations[0].groupedCount).toBe(2);
    expect(operations[0].primary.content).toEqual({ text: "Hello world" });
    expect(rawTraceOperations(entries)).toHaveLength(2);
  });

  it("keeps only decision-relevant operations in overview density", () => {
    const operations = buildTraceOperations([
      entry("start", "agent.turn.started", 0),
      entry("request", "model.request", 1, null, { model: "gpt-5.4" }),
      entry("response", "model.response", 2),
      entry("message", "message.assistant", 3, null, { text: "Done" }),
      entry("complete", "agent.turn.completed", 4),
    ]);

    expect(operationsForDensity(operations, "overview").map((operation) => operation.primary.record.kind)).toEqual([
      "agent.turn.started",
      "message.assistant",
      "agent.turn.completed",
    ]);
  });
});
