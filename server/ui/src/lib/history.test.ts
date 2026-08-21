import { describe, expect, it } from "vitest";
import { analyzeSession, previewForEntry, type TimelineEntry } from "./history";

function entry(kind: string, content: TimelineEntry["content"], overrides: Partial<TimelineEntry["record"]> = {}): TimelineEntry {
  return {
    record: {
      schema_version: "farfield.history.record.v2",
      id: `rec_${kind}`,
      conversation_id: "conv_test",
      kind,
      occurred_at: "2026-08-20T12:00:00Z",
      recorded_at: "2026-08-20T12:00:00Z",
      sequence: null,
      trace_id: "trace_test",
      span_id: null,
      parent_id: null,
      agent: "test-agent",
      tool: null,
      status: null,
      tags: {},
      content: { sha256: "a".repeat(64), size: 2, media_type: "application/json", key: "test" },
      ...overrides,
    },
    content,
  };
}

describe("analyzeSession", () => {
  it("derives execution metrics without inventing missing values", () => {
    const values = [
      entry("agent.turn.started", { run_id: "run_1" }, { status: "running" }),
      entry("message.user", { text: "Investigate the failure" }),
      entry("model.request", { model: "claude-sonnet-4-6" }),
      entry("tool.call", { name: "search" }, { tool: "search", status: "started" }),
      entry("agent.turn.completed", { usage: { input_tokens: 1200, output_tokens: 300 } }, { occurred_at: "2026-08-20T12:00:05Z", status: "completed" }),
    ];

    expect(analyzeSession(values)).toMatchObject({
      status: "complete",
      duration: 5000,
      turns: 1,
      toolCalls: 1,
      modelCalls: 1,
      inputTokens: 1200,
      outputTokens: 300,
      prompt: "Investigate the failure",
      models: ["claude-sonnet-4-6"],
      traces: ["trace_test"],
    });
  });

  it("gives failure status precedence over completion", () => {
    const values = [
      entry("agent.turn.completed", {}, { status: "completed" }),
      entry("tool.result", {}, { status: "failed" }),
    ];
    expect(analyzeSession(values).status).toBe("failed");
  });
});

describe("previewForEntry", () => {
  it("renders useful tool result summaries", () => {
    const value = entry("tool.result", { content: [{ title: "one" }, { title: "two" }] }, { tool: "web_search" });
    expect(previewForEntry(value)).toBe("2 results returned");
  });
});
