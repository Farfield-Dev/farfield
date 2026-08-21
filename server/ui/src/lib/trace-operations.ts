import { eventCategory, isReasoningEntry, previewForEntry, type JSONValue, type TimelineEntry } from "./history";

export type TraceDensity = "overview" | "trace" | "records";
export type TraceFocus = "all" | "tools" | "reasoning" | "slow" | "errors";

export type TraceOperation = {
  id: string;
  category: ReturnType<typeof eventCategory>;
  title: string;
  summary: string;
  entries: TimelineEntry[];
  primary: TimelineEntry;
  startedAt: string;
  endedAt: string;
  duration: number;
  status: string | null;
  groupedCount: number;
};

function asObject(value: JSONValue): Record<string, JSONValue> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value : null;
}

function humanize(value: string) {
  return value.replaceAll("_", " ").replaceAll("-", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

function toolVerb(tool: string) {
  const normalized = tool.toLowerCase();
  if (normalized === "github_search") return "Search GitHub";
  if (normalized === "web_search") return "Search web";
  if (normalized.includes("search")) return `Search ${humanize(tool.replace(/_?search$/i, ""))}`.trim();
  if (normalized.startsWith("read_")) return `Read ${humanize(tool.slice(5))}`;
  if (normalized.includes("query")) return humanize(tool);
  if (normalized.includes("fetch")) return humanize(tool);
  if (normalized === "exec") return "Run command";
  return `Run ${humanize(tool)}`;
}

function eventTitle(entry: TimelineEntry) {
  const content = asObject(entry.content);
  switch (entry.record.kind) {
    case "agent.turn.started": return "Started agent turn";
    case "agent.turn.completed": return "Completed agent turn";
    case "message.user": return "User request";
    case "message.assistant": return "Agent response";
    case "model.request": return typeof content?.model === "string" ? `Asked ${content.model}` : "Asked model";
    case "model.response": return "Received model response";
    case "tool.call": return toolVerb(entry.record.tool ?? String(content?.name ?? "tool"));
    case "tool.result": return `${humanize(entry.record.tool ?? "Tool")} result`;
    case "test.evidence": return "Verified evidence";
    default: return humanize(entry.record.kind);
  }
}

function operationStatus(entries: TimelineEntry[]) {
  const statuses = entries.map((entry) => entry.record.status?.toLowerCase()).filter(Boolean);
  if (statuses.some((status) => status === "error" || status === "failed" || status === "cancelled")) return "error";
  return statuses.at(-1) ?? null;
}

function makeOperation(entries: TimelineEntry[], groupedCount = entries.length): TraceOperation {
  const primary = entries[0];
  const last = entries.at(-1) ?? primary;
  const duration = Math.max(0, new Date(last.record.occurred_at).getTime() - new Date(primary.record.occurred_at).getTime());
  const result = entries.find((entry) => entry.record.kind === "tool.result");
  const summaryParts = [previewForEntry(primary)];
  if (result) summaryParts.push(previewForEntry(result));
  return {
    id: entries.map((entry) => entry.record.id).join("::"),
    category: eventCategory(primary.record.kind),
    title: eventTitle(primary),
    summary: summaryParts.filter(Boolean).join(" · "),
    entries,
    primary,
    startedAt: primary.record.occurred_at,
    endedAt: last.record.occurred_at,
    duration,
    status: operationStatus(entries),
    groupedCount,
  };
}

function toolUseID(entry: TimelineEntry) {
  const content = asObject(entry.content);
  for (const key of ["tool_use_id", "call_id", "id"]) {
    if (typeof content?.[key] === "string") return content[key] as string;
  }
  return null;
}

function matchingToolResult(entries: TimelineEntry[], callIndex: number, consumed: Set<number>) {
  const call = entries[callIndex];
  const callID = toolUseID(call);
  for (let index = callIndex + 1; index < entries.length; index += 1) {
    if (consumed.has(index)) continue;
    const candidate = entries[index];
    if (candidate.record.kind === "tool.call" && candidate.record.tool === call.record.tool) break;
    if (candidate.record.kind !== "tool.result") continue;
    const candidateID = toolUseID(candidate);
    const idsMatch = callID && candidateID && callID === candidateID;
    const toolsMatch = candidate.record.tool === call.record.tool && candidate.record.trace_id === call.record.trace_id;
    if (idsMatch || toolsMatch || index === callIndex + 1) return index;
  }
  return -1;
}

export function buildTraceOperations(entries: TimelineEntry[]) {
  const operations: TraceOperation[] = [];
  const consumed = new Set<number>();

  for (let index = 0; index < entries.length; index += 1) {
    if (consumed.has(index)) continue;
    const entry = entries[index];

    if (entry.record.kind === "tool.call") {
      const resultIndex = matchingToolResult(entries, index, consumed);
      if (resultIndex >= 0) {
        consumed.add(resultIndex);
        operations.push(makeOperation([entry, entries[resultIndex]]));
        continue;
      }
    }

    if (entry.record.kind === "message.assistant") {
      const chunks = [entry];
      let next = index + 1;
      while (next < entries.length && entries[next].record.kind === "message.assistant" && entries[next].record.trace_id === entry.record.trace_id) {
        chunks.push(entries[next]);
        consumed.add(next);
        next += 1;
      }
      if (chunks.length > 1) {
        const texts = chunks.map((chunk) => asObject(chunk.content)?.text).filter((text): text is string => typeof text === "string");
        const merged = structuredClone(entry);
        merged.content = { ...(asObject(merged.content) ?? {}), text: texts.join("") };
        operations.push(makeOperation([merged], chunks.length));
        continue;
      }
    }

    operations.push(makeOperation([entry], 1));
  }

  return operations;
}

export function rawTraceOperations(entries: TimelineEntry[]) {
  return entries.map((entry) => makeOperation([entry], 1));
}

export function operationsForDensity(operations: TraceOperation[], density: TraceDensity) {
  if (density !== "overview") return operations;
  let finalAssistant = -1;
  for (let index = operations.length - 1; index >= 0; index -= 1) {
    if (operations[index].primary.record.kind === "message.assistant") {
      finalAssistant = index;
      break;
    }
  }
  return operations.filter((operation, index) => {
    const kind = operation.primary.record.kind;
    return operation.category === "tool"
      || operation.category === "test"
      || kind === "agent.turn.started"
      || kind === "agent.turn.completed"
      || kind === "message.user"
      || index === finalAssistant
      || operation.status === "error";
  });
}

export function operationMatchesFocus(operation: TraceOperation, focus: TraceFocus) {
  if (focus === "tools") return operation.category === "tool";
  if (focus === "reasoning") return operation.entries.some(isReasoningEntry);
  if (focus === "slow") return operation.duration >= 1_000;
  if (focus === "errors") return operation.status === "error";
  return true;
}
