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

  it("includes explicit reasoning records and token usage for UI coverage", () => {
    const timeline = demoTimeline(demoConversations[0].id);
    const completion = timeline.find((entry) => entry.record.kind === "agent.turn.completed");
    const usage = (completion?.content as { usage?: { output_tokens_details?: { reasoning_tokens?: number } } }).usage;

    expect(timeline.some((entry) => entry.record.kind === "model.reasoning")).toBe(true);
    expect(usage?.output_tokens_details?.reasoning_tokens).toBeGreaterThan(0);
  });

  it("covers both recovered tool errors and terminal session failures", () => {
    const recovered = demoTimeline("conv_demo_release-readiness-audit");
    const recoveredFailureIndex = recovered.findIndex((entry) => entry.record.kind === "tool.result" && entry.record.status === "failed");
    const recoveredContent = recovered[recoveredFailureIndex]?.content as { ok?: boolean; error?: { retryable?: boolean } };

    expect(recoveredFailureIndex).toBeGreaterThan(0);
    expect(recoveredContent).toMatchObject({ ok: false, error: { retryable: true } });
    expect(recovered.slice(recoveredFailureIndex + 1).some((entry) => entry.record.kind === "tool.result" && entry.record.status === "complete")).toBe(true);
    expect(recovered.some((entry) => entry.record.kind === "message.assistant" && JSON.stringify(entry.content).includes("**exec** call failed"))).toBe(true);
    expect(recovered.at(-1)?.record).toMatchObject({ kind: "agent.turn.completed", status: "complete" });

    const terminal = demoTimeline("conv_demo_checkout-incident-triage");
    const terminalFailure = terminal.find((entry) => entry.record.kind === "tool.result" && entry.record.status === "failed");
    expect(terminalFailure?.content).toMatchObject({ ok: false, error: { retryable: false } });
    expect(terminal.at(-1)?.record).toMatchObject({ kind: "agent.turn.completed", status: "failed" });
  });
});
