import { describe, expect, it } from "vitest";
import type { HistoryRecord, TimelineEntry } from "./history";
import { buildReviewBlocks } from "./conversation-review";

function entry(id: string, kind: string, second: number, tool: string | null = null, status: string | null = null): TimelineEntry {
  const occurredAt = new Date(Date.UTC(2026, 7, 20, 12, 0, second)).toISOString();
  const record: HistoryRecord = {
    schema_version: "2",
    id,
    conversation_id: "conv_review",
    kind,
    occurred_at: occurredAt,
    recorded_at: occurredAt,
    sequence: second,
    trace_id: "trace_review",
    span_id: `span_${id}`,
    parent_id: null,
    agent: "review-agent",
    tool,
    status,
    tags: {},
    content: { sha256: id.padEnd(64, "0"), size: 10, media_type: "application/json", key: id },
  };
  return { record, content: kind.startsWith("message.") ? { text: id } : {} };
}

describe("conversation review", () => {
  it("preserves message records and bundles only consecutive non-message activity", () => {
    const blocks = buildReviewBlocks([
      entry("start", "agent.turn.started", 0),
      entry("request", "message.user", 1),
      entry("call", "tool.call", 2, "read_file"),
      entry("result", "tool.result", 4, "read_file", "complete"),
      entry("answer", "message.assistant", 5),
      entry("complete", "agent.turn.completed", 6),
    ]);

    expect(blocks.map((block) => block.type)).toEqual(["activity", "message", "activity", "message", "activity"]);
    const activity = blocks[2];
    expect(activity.type).toBe("activity");
    if (activity.type === "activity") {
      expect(activity.entries.map((value) => value.record.id)).toEqual(["call", "result"]);
      expect(activity.duration).toBe(2_000);
      expect(activity.tools).toEqual(["read_file"]);
    }
  });

  it("marks only the latest assistant message", () => {
    const blocks = buildReviewBlocks([
      entry("one", "message.assistant", 1),
      entry("two", "message.assistant", 2),
    ]).filter((block) => block.type === "message");

    expect(blocks.map((block) => block.type === "message" && block.isLatestAssistant)).toEqual([false, true]);
  });

  it("reports only explicit error statuses", () => {
    const [block] = buildReviewBlocks([
      entry("call", "tool.call", 1, "exec"),
      entry("failed", "tool.result", 2, "exec", "failed"),
      entry("unknown", "event.custom", 3, null, null),
    ]);

    expect(block.type === "activity" && block.errors).toBe(1);
  });

  it("counts explicitly captured reasoning separately from other model events", () => {
    const [block] = buildReviewBlocks([
      entry("request", "model.request", 1),
      entry("thinking", "model.reasoning", 2),
      entry("response", "model.response", 3),
    ]);

    expect(block.type === "activity" && block.reasoningEvents).toBe(1);
    expect(block.type === "activity" && block.modelEvents).toBe(2);
  });
});
