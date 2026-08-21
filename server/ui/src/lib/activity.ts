import type { ConversationSummary } from "./history";

export type DailyActivity = {
  date: Date;
  key: string;
  sessions: number;
  agents: number;
  events: number;
};

function localDateKey(date: Date) {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}
export function buildDailyActivity(conversations: ConversationSummary[], days = 14, now = new Date()): DailyActivity[] {
  const safeDays = Math.max(1, Math.floor(days));
  const start = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  start.setDate(start.getDate() - safeDays + 1);

  const buckets = Array.from({ length: safeDays }, (_, index) => {
    const date = new Date(start);
    date.setDate(start.getDate() + index);
    return { date, key: localDateKey(date), sessions: 0, agents: new Set<string>(), events: 0 };
  });
  const byKey = new Map(buckets.map((bucket) => [bucket.key, bucket]));

  for (const conversation of conversations) {
    const startedAt = new Date(conversation.first_seen_at);
    if (!Number.isFinite(startedAt.getTime())) continue;
    const bucket = byKey.get(localDateKey(startedAt));
    if (!bucket) continue;
    bucket.sessions += 1;
    bucket.events += conversation.record_count;
    conversation.agents.forEach((agent) => bucket.agents.add(agent));
  }

  return buckets.map(({ agents, ...bucket }) => ({ ...bucket, agents: agents.size }));
}
