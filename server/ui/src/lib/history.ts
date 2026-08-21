import { demoConversations, demoTimeline, isDemoMode } from "./demo-data";

export type JSONValue = null | boolean | number | string | JSONValue[] | { [key: string]: JSONValue };

export type ConversationSummary = {
  id: string;
  record_count: number;
  first_seen_at: string;
  last_seen_at: string;
  agents: string[];
  kinds: string[];
};

export type HistoryRecord = {
  schema_version: string;
  id: string;
  conversation_id: string;
  kind: string;
  occurred_at: string;
  recorded_at: string;
  sequence: number | null;
  trace_id: string | null;
  span_id: string | null;
  parent_id: string | null;
  agent: string | null;
  tool: string | null;
  status: string | null;
  tags: Record<string, string>;
  content: {
    sha256: string;
    size: number;
    media_type: string;
    key: string;
    storage?: string;
    entry_index?: number;
  };
  record_sha256?: string;
};

export type TimelineEntry = {
  record: HistoryRecord;
  content: JSONValue;
};

export type SessionAnalysis = {
  status: "complete" | "running" | "failed" | "captured";
  duration: number;
  turns: number;
  toolCalls: number;
  modelCalls: number;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  reasoningEvents: number;
  prompt: string | null;
  models: string[];
  traces: string[];
};

type ErrorPayload = { error?: { message?: string } };

async function readJSON<T>(response: Response): Promise<T> {
  const value = (await response.json()) as T & ErrorPayload;
  if (!response.ok) throw new Error(value.error?.message ?? `Request failed with ${response.status}`);
  return value;
}

export async function fetchConversations(signal?: AbortSignal) {
  if (isDemoMode()) return structuredClone(demoConversations);
  return readJSON<ConversationSummary[]>(await fetch("/v1/history/conversations?limit=100", { signal }));
}

export async function fetchTimeline(conversationID: string, signal?: AbortSignal) {
  if (isDemoMode()) return demoTimeline(conversationID);
  return readJSON<TimelineEntry[]>(
    await fetch(`/v1/history/conversations/${encodeURIComponent(conversationID)}/timeline`, { signal }),
  );
}

function asObject(value: JSONValue): Record<string, JSONValue> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value : null;
}

function asNumber(value: JSONValue | undefined) {
  return typeof value === "number" ? value : 0;
}

function firstNumber(...values: (JSONValue | undefined)[]) {
  return values.find((value): value is number => typeof value === "number") ?? 0;
}

export function isReasoningEntry(entry: TimelineEntry) {
  const kind = entry.record.kind.toLowerCase();
  if (kind.includes("reasoning") || kind.includes("thinking")) return true;
  const content = asObject(entry.content);
  const type = typeof content?.type === "string" ? content.type.toLowerCase() : "";
  return type === "reasoning" || type === "thinking";
}

export function reasoningTextForEntry(entry: TimelineEntry) {
  const content = asObject(entry.content);
  for (const key of ["text", "reasoning", "thinking", "summary"]) {
    if (typeof content?.[key] === "string") return content[key] as string;
  }
  return null;
}

export function analyzeSession(entries: TimelineEntry[]): SessionAnalysis {
  const first = entries[0]?.record.occurred_at;
  const last = entries.at(-1)?.record.occurred_at;
  const statuses = new Set(entries.map((entry) => entry.record.status?.toLowerCase()).filter((status): status is string => Boolean(status)));
  const terminal = [...entries].reverse().find((entry) => entry.record.kind === "agent.turn.completed");
  const failureStatuses = new Set(["failed", "error", "cancelled"]);
  const failed = terminal
    ? failureStatuses.has(terminal.record.status?.toLowerCase() ?? "")
    : [...statuses].some((status) => failureStatuses.has(status));
  const running = statuses.has("running") && !terminal;
  const complete = Boolean(terminal) && !failed;
  const models = new Set<string>();
  const traces = new Set<string>();
  let inputTokens = 0;
  let outputTokens = 0;
  let reasoningTokens = 0;
  let cacheReadTokens = 0;
  let prompt: string | null = null;

  for (const entry of entries) {
    if (entry.record.trace_id) traces.add(entry.record.trace_id);
    const content = asObject(entry.content);
    if (!content) continue;
    if (!prompt && entry.record.kind === "message.user" && typeof content.text === "string") prompt = content.text;
    if (entry.record.kind === "model.request" && typeof content.model === "string") models.add(content.model);
    if (entry.record.kind === "agent.turn.completed") {
      const usage = asObject(content.usage);
      inputTokens += asNumber(usage?.input_tokens);
      outputTokens += asNumber(usage?.output_tokens);
      const outputDetails = asObject(usage?.output_tokens_details ?? null);
      const completionDetails = asObject(usage?.completion_tokens_details ?? null);
      const inputDetails = asObject(usage?.input_tokens_details ?? null);
      reasoningTokens += firstNumber(usage?.reasoning_tokens, outputDetails?.reasoning_tokens, completionDetails?.reasoning_tokens);
      cacheReadTokens += firstNumber(usage?.cache_read_input_tokens, usage?.cached_input_tokens, inputDetails?.cached_tokens);
    }
  }

  return {
    status: failed ? "failed" : running ? "running" : complete ? "complete" : "captured",
    duration: first && last ? new Date(last).getTime() - new Date(first).getTime() : 0,
    turns: entries.filter((entry) => entry.record.kind === "agent.turn.started").length,
    toolCalls: entries.filter((entry) => entry.record.kind === "tool.call").length,
    modelCalls: entries.filter((entry) => entry.record.kind === "model.request").length,
    inputTokens,
    outputTokens,
    reasoningTokens,
    cacheReadTokens,
    reasoningEvents: entries.filter(isReasoningEntry).length,
    prompt,
    models: [...models],
    traces: [...traces],
  };
}

export function previewForEntry(entry: TimelineEntry) {
  const content = asObject(entry.content);
  if (!content) return String(entry.content ?? "No content");
  if (typeof content.text === "string") return content.text;
  if (entry.record.kind === "tool.call") {
    const input = asObject(content.input);
    if (typeof input?.query === "string") return input.query;
    return `${entry.record.tool ?? content.name ?? "Tool"} called`;
  }
  if (entry.record.kind === "tool.result" && Array.isArray(content.content)) {
    return `${content.content.length} result${content.content.length === 1 ? "" : "s"} returned`;
  }
  if (entry.record.kind === "tool.result") {
    const error = asObject(content.error);
    if (typeof error?.message === "string") return error.message;
  }
  if (entry.record.kind === "model.request") {
    return [content.model, content.provider].filter((value) => typeof value === "string").join(" · ") || "Model request";
  }
  if (entry.record.kind === "model.response") {
    return [content.model, content.stop_reason].filter((value) => typeof value === "string").join(" · ") || "Model response";
  }
  if (typeof content.prompt_name === "string") return content.prompt_name;
  const entries = Object.entries(content).filter(([key]) => key !== "encrypted_content");
  return entries.slice(0, 2).map(([key, value]) => `${key}: ${String(value)}`).join(" · ") || "Structured event";
}

export function eventCategory(kind: string) {
  if (kind.startsWith("message.")) return "message" as const;
  if (kind.startsWith("model.")) return "model" as const;
  if (kind.startsWith("tool.")) return "tool" as const;
  if (kind.startsWith("agent.")) return "agent" as const;
  if (kind.startsWith("test.")) return "test" as const;
  return "event" as const;
}
