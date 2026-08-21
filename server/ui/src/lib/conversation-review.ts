import { isReasoningEntry, type TimelineEntry } from "./history";

export type ReviewMessageBlock = {
  type: "message";
  id: string;
  entry: TimelineEntry;
  role: "user" | "assistant";
  isLatestAssistant: boolean;
};

export type ReviewActivityBlock = {
  type: "activity";
  id: string;
  entries: TimelineEntry[];
  duration: number;
  toolCalls: number;
  toolResults: number;
  modelEvents: number;
  reasoningEvents: number;
  errors: number;
  tools: string[];
};

export type ReviewBlock = ReviewMessageBlock | ReviewActivityBlock;

function isMessage(entry: TimelineEntry) {
  return entry.record.kind === "message.user" || entry.record.kind === "message.assistant";
}

function hasError(entry: TimelineEntry) {
  const status = entry.record.status?.toLowerCase();
  return status === "error" || status === "failed" || status === "cancelled";
}

function activityBlock(entries: TimelineEntry[]): ReviewActivityBlock {
  const first = entries[0];
  const last = entries.at(-1) ?? first;
  return {
    type: "activity",
    id: `activity:${first.record.id}:${last.record.id}`,
    entries,
    duration: Math.max(0, new Date(last.record.occurred_at).getTime() - new Date(first.record.occurred_at).getTime()),
    toolCalls: entries.filter((entry) => entry.record.kind === "tool.call").length,
    toolResults: entries.filter((entry) => entry.record.kind === "tool.result").length,
    modelEvents: entries.filter((entry) => entry.record.kind.startsWith("model.") && !isReasoningEntry(entry)).length,
    reasoningEvents: entries.filter(isReasoningEntry).length,
    errors: entries.filter(hasError).length,
    tools: [...new Set(entries.map((entry) => entry.record.tool).filter((tool): tool is string => Boolean(tool)))],
  };
}

export function buildReviewBlocks(entries: TimelineEntry[]): ReviewBlock[] {
  const latestAssistantID = [...entries].reverse().find((entry) => entry.record.kind === "message.assistant")?.record.id;
  const blocks: ReviewBlock[] = [];
  let pending: TimelineEntry[] = [];

  const flushActivity = () => {
    if (pending.length === 0) return;
    blocks.push(activityBlock(pending));
    pending = [];
  };

  for (const entry of entries) {
    if (!isMessage(entry)) {
      pending.push(entry);
      continue;
    }
    flushActivity();
    blocks.push({
      type: "message",
      id: entry.record.id,
      entry,
      role: entry.record.kind === "message.user" ? "user" : "assistant",
      isLatestAssistant: entry.record.id === latestAssistantID,
    });
  }
  flushActivity();
  return blocks;
}
