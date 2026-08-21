import type { JSONValue, TimelineEntry } from "./history";

export type FilterField = "kind" | "agent" | "tool" | "model" | "status" | "trace" | "offset" | "size" | "tokens" | "has" | `tag.${string}`;
export type ComparisonOperator = "contains" | "eq" | "gt" | "gte" | "lt" | "lte";

export type FilterClause = {
  type: "field" | "text";
  raw: string;
  negated: boolean;
  field?: FilterField;
  operator: ComparisonOperator;
  value: string;
};

export type ParsedTimelineQuery = {
  clauses: FilterClause[];
  errors: string[];
};

export type ContextMode = "matches" | "context" | "full";

export const filterFields = ["kind", "agent", "tool", "model", "status", "trace", "offset", "size", "tokens", "has"] as const;

function tokenize(query: string) {
  const tokens: string[] = [];
  const errors: string[] = [];
  let current = "";
  let quoted = false;

  for (const character of query.trim()) {
    if (character === '"') {
      quoted = !quoted;
      current += character;
      continue;
    }
    if (/\s/.test(character) && !quoted) {
      if (current) tokens.push(current);
      current = "";
      continue;
    }
    current += character;
  }
  if (current) tokens.push(current);
  if (quoted) errors.push("Close the open quote to apply this query.");
  return { tokens, errors };
}

function unquote(value: string) {
  return value.startsWith('"') && value.endsWith('"') ? value.slice(1, -1) : value;
}

function isFilterField(value: string): value is FilterField {
  return filterFields.includes(value as (typeof filterFields)[number]) || value.startsWith("tag.");
}

export function parseTimelineQuery(query: string): ParsedTimelineQuery {
  const { tokens, errors } = tokenize(query);
  const clauses: FilterClause[] = [];

  for (const rawToken of tokens) {
    const negated = rawToken.startsWith("-");
    const token = negated ? rawToken.slice(1) : rawToken;
    const separator = token.indexOf(":");
    if (separator < 1) {
      const value = unquote(token);
      if (value) clauses.push({ type: "text", raw: rawToken, negated, operator: "contains", value });
      continue;
    }

    const fieldName = token.slice(0, separator).toLowerCase();
    if (!isFilterField(fieldName)) {
      errors.push(`Unknown field “${fieldName}”.`);
      continue;
    }
    let value = unquote(token.slice(separator + 1));
    if (!value) {
      errors.push(`Add a value after ${fieldName}:.`);
      continue;
    }
    let operator: ComparisonOperator = "contains";
    const comparison = value.match(/^(>=|<=|>|<|=)(.+)$/);
    if (comparison) {
      operator = comparison[1] === ">=" ? "gte" : comparison[1] === "<=" ? "lte" : comparison[1] === ">" ? "gt" : comparison[1] === "<" ? "lt" : "eq";
      value = comparison[2];
    }
    clauses.push({ type: "field", raw: rawToken, negated, field: fieldName, operator, value });
  }

  return { clauses, errors };
}

function asObject(value: JSONValue): Record<string, JSONValue> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value : null;
}

function numericValue(value: string, field: FilterField) {
  const match = value.trim().toLowerCase().match(/^([\d.]+)\s*([a-z]*)$/);
  if (!match) return Number.NaN;
  const amount = Number(match[1]);
  const unit = match[2];
  if (field === "offset") {
    const multiplier = unit === "h" ? 3_600_000 : unit === "m" || unit === "min" ? 60_000 : unit === "s" ? 1_000 : 1;
    return amount * multiplier;
  }
  if (field === "size") {
    const multiplier = unit === "mb" ? 1_048_576 : unit === "kb" || unit === "k" ? 1_024 : 1;
    return amount * multiplier;
  }
  if (field === "tokens") return amount * (unit === "m" ? 1_000_000 : unit === "k" ? 1_000 : 1);
  return amount;
}

function compareNumber(actual: number, operator: ComparisonOperator, expected: number) {
  if (!Number.isFinite(expected)) return false;
  if (operator === "gt") return actual > expected;
  if (operator === "gte") return actual >= expected;
  if (operator === "lt") return actual < expected;
  if (operator === "lte") return actual <= expected;
  return actual === expected;
}

function compareText(actual: string | null | undefined, expected: string, operator: ComparisonOperator): boolean {
  if (!actual) return false;
  const normalized = actual.toLowerCase();
  const target = expected.toLowerCase();
  if (target.includes("|") && operator === "contains") return target.split("|").some((candidate) => compareText(actual, candidate, "contains"));
  if (operator === "eq") return normalized === target;
  if (target.includes("*")) {
    const pattern = target.split("*").map((part) => part.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join(".*");
    return new RegExp(`^${pattern}$`, "i").test(actual);
  }
  return normalized.includes(target);
}

function modelForEntry(entry: TimelineEntry) {
  const content = asObject(entry.content);
  return typeof content?.model === "string" ? content.model : null;
}

function tokensForEntry(entry: TimelineEntry) {
  const content = asObject(entry.content);
  const usage = content ? asObject(content.usage ?? null) : null;
  return [usage?.input_tokens, usage?.output_tokens, usage?.cache_read_input_tokens]
    .reduce<number>((sum, value) => sum + (typeof value === "number" ? value : 0), 0);
}

export function entrySearchText(entry: TimelineEntry) {
  return [
    entry.record.kind,
    entry.record.agent,
    entry.record.tool,
    entry.record.status,
    entry.record.trace_id,
    modelForEntry(entry),
    ...Object.entries(entry.record.tags).flat(),
    JSON.stringify(entry.content),
  ].filter(Boolean).join(" ").toLowerCase();
}

function fieldMatches(entry: TimelineEntry, clause: FilterClause, timelineStart: number) {
  const field = clause.field;
  if (!field) return false;
  if (field === "has") {
    const target = clause.value.toLowerCase();
    if (target === "trace") return Boolean(entry.record.trace_id);
    if (target === "tags") return Object.keys(entry.record.tags).length > 0;
    if (target === "tool") return Boolean(entry.record.tool);
    if (target === "agent") return Boolean(entry.record.agent);
    if (target === "status") return Boolean(entry.record.status);
    return false;
  }
  if (field.startsWith("tag.")) return compareText(entry.record.tags[field.slice(4)], clause.value, clause.operator);
  if (field === "offset") return compareNumber(new Date(entry.record.occurred_at).getTime() - timelineStart, clause.operator, numericValue(clause.value, field));
  if (field === "size") return compareNumber(entry.record.content.size, clause.operator, numericValue(clause.value, field));
  if (field === "tokens") return compareNumber(tokensForEntry(entry), clause.operator, numericValue(clause.value, field));
  const actual = field === "kind" ? entry.record.kind
    : field === "agent" ? entry.record.agent
    : field === "tool" ? entry.record.tool
    : field === "model" ? modelForEntry(entry)
    : field === "status" ? entry.record.status
    : entry.record.trace_id;
  return compareText(actual, clause.value, clause.operator);
}

export function timelineEntryMatches(entry: TimelineEntry, parsed: ParsedTimelineQuery, timelineStart: number) {
  const searchable = entrySearchText(entry);
  return parsed.clauses.every((clause) => {
    const matches = clause.type === "text" ? searchable.includes(clause.value.toLowerCase()) : fieldMatches(entry, clause, timelineStart);
    return clause.negated ? !matches : matches;
  });
}

export function indexesForContext(length: number, matches: Set<number>, mode: ContextMode) {
  if (mode === "full") return new Set(Array.from({ length }, (_, index) => index));
  if (mode === "matches") return matches;
  const context = new Set<number>();
  for (const index of matches) {
    for (let current = Math.max(0, index - 1); current <= Math.min(length - 1, index + 1); current += 1) context.add(current);
  }
  return context;
}

export function observedFilterValues(entries: TimelineEntry[], field: string) {
  const values = new Set<string>();
  for (const entry of entries) {
    const value = field === "kind" ? entry.record.kind
      : field === "agent" ? entry.record.agent
      : field === "tool" ? entry.record.tool
      : field === "model" ? modelForEntry(entry)
      : field === "status" ? entry.record.status
      : field === "trace" ? entry.record.trace_id
      : field.startsWith("tag.") ? entry.record.tags[field.slice(4)]
      : null;
    if (value) values.add(value);
  }
  return [...values].sort();
}

export function quoteFilterValue(value: string) {
  return /\s/.test(value) ? `"${value.replaceAll('"', '\\"')}"` : value;
}
