import { describe, expect, it } from "vitest";
import type { TimelineEntry } from "./history";
import { indexesForContext, parseTimelineQuery, timelineEntryMatches } from "./timeline-filter";

const entry: TimelineEntry = {
  record: {
    schema_version: "1",
    id: "event-1",
    conversation_id: "conversation",
    kind: "tool.call",
    occurred_at: "2026-08-20T12:00:03.000Z",
    recorded_at: "2026-08-20T12:00:03.000Z",
    sequence: 1,
    trace_id: "trace-abc",
    span_id: null,
    parent_id: null,
    agent: "research agent",
    tool: "web_search",
    status: "running",
    tags: { prompt: "landscape" },
    content: { sha256: "hash", size: 2048, media_type: "application/json", key: "key" },
  },
  content: { model: "claude-sonnet", input: { query: "durable agents" } },
};

describe("timeline filtering", () => {
  it("parses structured, quoted, comparison, and negated clauses", () => {
    const parsed = parseTimelineQuery('agent:"research agent" size:>=2kb -status:error durable');
    expect(parsed.errors).toEqual([]);
    expect(parsed.clauses).toMatchObject([
      { field: "agent", value: "research agent", negated: false },
      { field: "size", value: "2kb", operator: "gte" },
      { field: "status", value: "error", negated: true },
      { type: "text", value: "durable" },
    ]);
  });

  it("matches fields, tags, numeric units, bare text, and negation", () => {
    const start = new Date("2026-08-20T12:00:00.000Z").getTime();
    expect(timelineEntryMatches(entry, parseTimelineQuery("kind:tool tool:web offset:>=3s size:>=2kb tag.prompt:land"), start)).toBe(true);
    expect(timelineEntryMatches(entry, parseTimelineQuery("durable -status:error has:trace"), start)).toBe(true);
    expect(timelineEntryMatches(entry, parseTimelineQuery("status:complete"), start)).toBe(false);
  });

  it("expands matches to adjacent context without exceeding bounds", () => {
    expect([...indexesForContext(6, new Set([0, 3]), "context")]).toEqual([0, 1, 2, 3, 4]);
    expect([...indexesForContext(3, new Set([1]), "full")]).toEqual([0, 1, 2]);
  });

  it("reports unknown fields and incomplete values", () => {
    expect(parseTimelineQuery("wat:value tool:").errors).toEqual(["Unknown field “wat”.", "Add a value after tool:."]);
    expect(timelineEntryMatches(entry, parseTimelineQuery("status:complete wat:value"), 0)).toBe(false);
  });
});
