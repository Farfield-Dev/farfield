import { describe, expect, it } from "vitest";
import type { ConversationSummary } from "./history";
import { buildDailyActivity } from "./activity";

function conversation(overrides: Partial<ConversationSummary>): ConversationSummary {
  return {
    id: "session",
    record_count: 1,
    first_seen_at: "2026-08-20T12:00:00",
    last_seen_at: "2026-08-20T12:01:00",
    agents: [],
    kinds: [],
    ...overrides,
  };
}

describe("buildDailyActivity", () => {
  it("buckets sessions, events, and unique agents by local start date", () => {
    const result = buildDailyActivity([
      conversation({ id: "a", record_count: 8, agents: ["researcher", "writer"] }),
      conversation({ id: "b", record_count: 5, agents: ["researcher"] }),
      conversation({ id: "c", first_seen_at: "2026-08-19T09:00:00", record_count: 3, agents: ["reviewer"] }),
    ], 3, new Date("2026-08-20T18:00:00"));

    expect(result.map(({ key, sessions, agents, events }) => ({ key, sessions, agents, events }))).toEqual([
      { key: "2026-08-18", sessions: 0, agents: 0, events: 0 },
      { key: "2026-08-19", sessions: 1, agents: 1, events: 3 },
      { key: "2026-08-20", sessions: 2, agents: 2, events: 13 },
    ]);
  });

  it("ignores invalid and out-of-window sessions", () => {
    const result = buildDailyActivity([
      conversation({ first_seen_at: "invalid" }),
      conversation({ first_seen_at: "2026-08-01T12:00:00" }),
    ], 2, new Date("2026-08-20T18:00:00"));

    expect(result.every((day) => day.sessions === 0 && day.events === 0 && day.agents === 0)).toBe(true);
  });
});
