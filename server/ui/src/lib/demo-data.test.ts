import { describe, expect, it } from "vitest";
import { demoConversations, demoTimeline } from "./demo-data";

describe("demo data", () => {
  it("provides a dense two-week workload with varied agents and models", () => {
    const activeDays = new Set(demoConversations.map((conversation) => conversation.first_seen_at.slice(0, 10)));
    const agents = new Set(demoConversations.flatMap((conversation) => conversation.agents));
    const models = new Set(demoConversations.flatMap((conversation) => demoTimeline(conversation.id)
      .filter((entry) => entry.record.kind === "model.request")
      .map((entry) => (entry.content as { model: string }).model)));

    expect(demoConversations.length).toBeGreaterThanOrEqual(20);
    expect(activeDays.size).toBe(14);
    expect(agents.size).toBeGreaterThanOrEqual(8);
    expect(models.size).toBeGreaterThanOrEqual(4);
  });

  it("keeps every session summary reconciled with its generated timeline", () => {
    for (const conversation of demoConversations) {
      const timeline = demoTimeline(conversation.id);
      expect(timeline).toHaveLength(conversation.record_count);
      expect(timeline.every((entry) => entry.record.conversation_id === conversation.id)).toBe(true);
      expect(timeline[0].record.occurred_at).toBe(conversation.first_seen_at);
      expect(timeline.at(-1)?.record.occurred_at).toBe(conversation.last_seen_at);
    }
  });
});
