import {
  Bot,
  Box,
  Check,
  CheckCircle2,
  CircleDot,
  Clock3,
  Copy,
  Database,
  ExternalLink,
  FileJson2,
  Fingerprint,
  Gauge,
  MessageSquare,
  Search,
  Sparkles,
  TerminalSquare,
  UserRound,
  Wrench,
  XCircle,
  Zap,
} from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import ReactMarkdown from "react-markdown";
import { Badge } from "../design-system/badge";
import { Button } from "../design-system/button";
import { Panel } from "../design-system/panel";
import {
  analyzeSession,
  eventCategory,
  previewForEntry,
  type ConversationSummary,
  type JSONValue,
  type TimelineEntry,
} from "../lib/history";
import { cn, formatDuration, formatNumber, formatTimestamp, shortID } from "../lib/utils";
import {
  filterFields,
  indexesForContext,
  observedFilterValues,
  parseTimelineQuery,
  quoteFilterValue,
  timelineEntryMatches,
  type ContextMode,
  type FilterField,
} from "../lib/timeline-filter";

type InspectorProps = {
  conversation: ConversationSummary | null;
  entries: TimelineEntry[];
  loading: boolean;
  error: string | null;
  onRetry: () => void;
  onCompare: () => void;
};

type DetailTab = "rendered" | "json";
type DisplayEntry = TimelineEntry & { groupedCount?: number };

const contextLabels: Record<ContextMode, string> = { matches: "Matches", context: "Context", full: "Full" };
const facetDefinitions = [
  { label: "Errors", token: "status:error|failed|cancelled", query: "status:error|failed|cancelled" },
  { label: "Tools", token: "kind:tool.", query: "kind:tool." },
  { label: "Messages", token: "kind:message.", query: "kind:message." },
  { label: "Model I/O", token: "kind:model.", query: "kind:model." },
  { label: "Turns", token: "kind:agent.turn", query: "kind:agent.turn" },
] as const;

function initialContextMode(): ContextMode {
  const value = new URLSearchParams(window.location.search).get("timeline_context");
  return value === "matches" || value === "full" ? value : "context";
}

function appendQueryToken(query: string, token: string) {
  const normalized = query.trim();
  return normalized ? `${normalized} ${token}` : token;
}

function removeQueryToken(query: string, token: string) {
  return query.split(/\s+/).filter((value) => value !== token).join(" ");
}

const categoryMeta = {
  agent: { icon: Bot, tone: "neutral" as const, label: "Agent" },
  message: { icon: MessageSquare, tone: "neutral" as const, label: "Message" },
  model: { icon: Sparkles, tone: "neutral" as const, label: "Model" },
  tool: { icon: Wrench, tone: "neutral" as const, label: "Tool" },
  test: { icon: TerminalSquare, tone: "neutral" as const, label: "Evidence" },
  event: { icon: CircleDot, tone: "neutral" as const, label: "Event" },
};

const timelineIconClasses = {
  agent: "text-ink-muted",
  message: "text-ink-muted",
  model: "text-ink-muted",
  tool: "text-ink-muted",
  test: "text-ink-muted",
  event: "text-ink-muted",
};

function asObject(value: JSONValue): Record<string, JSONValue> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value : null;
}

function groupTimeline(entries: TimelineEntry[]): DisplayEntry[] {
  const result: DisplayEntry[] = [];
  for (const entry of entries) {
    const previous = result.at(-1);
    if (
      entry.record.kind === "message.assistant" &&
      previous?.record.kind === "message.assistant" &&
      previous.record.trace_id === entry.record.trace_id
    ) {
      const previousContent = asObject(previous.content);
      const content = asObject(entry.content);
      if (previousContent && content && typeof previousContent.text === "string" && typeof content.text === "string") {
        previous.content = { ...previousContent, text: `${previousContent.text}${content.text}` };
        previous.groupedCount = (previous.groupedCount ?? 1) + 1;
        previous.record = { ...previous.record, occurred_at: entry.record.occurred_at };
        continue;
      }
    }
    result.push({ ...entry, record: { ...entry.record }, content: structuredClone(entry.content), groupedCount: 1 });
  }
  return result;
}

function eventTitle(entry: TimelineEntry) {
  const content = asObject(entry.content);
  switch (entry.record.kind) {
    case "agent.turn.started": return "Agent turn started";
    case "agent.turn.completed": return "Agent turn completed";
    case "message.user": return "User message";
    case "message.assistant": return "Assistant response";
    case "model.request": return typeof content?.model === "string" ? content.model : "Model request";
    case "model.response": return "Model response";
    case "tool.call": return `${entry.record.tool ?? content?.name ?? "Tool"} call`;
    case "tool.result": return `${entry.record.tool ?? "Tool"} result`;
    default: return entry.record.kind;
  }
}

function promptTitle(prompt: string | null, conversation: ConversationSummary) {
  if (!prompt) return conversation.agents[0]?.replaceAll("-", " ") ?? "Captured session";
  const clean = prompt.replace(/\s+/g, " ").trim();
  const sentence = clean.match(/^(.{24,105}?[.!?])(?:\s|$)/)?.[1];
  return sentence ?? `${clean.slice(0, 104)}${clean.length > 104 ? "…" : ""}`;
}

export function TraceInspector({ conversation, entries, loading, error, onRetry, onCompare }: InspectorProps) {
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [timelineQuery, setTimelineQuery] = useState(() => new URLSearchParams(window.location.search).get("timeline") ?? "");
  const [contextMode, setContextMode] = useState<ContextMode>(initialContextMode);
  const [queryFocused, setQueryFocused] = useState(false);
  const [suggestionIndex, setSuggestionIndex] = useState(0);
  const [detailTab, setDetailTab] = useState<DetailTab>("rendered");
  const [copied, setCopied] = useState(false);

  const groupedEntries = useMemo(() => groupTimeline(entries), [entries]);
  const parsedQuery = useMemo(() => parseTimelineQuery(timelineQuery), [timelineQuery]);
  const queryActive = parsedQuery.clauses.length > 0;
  const timelineStart = groupedEntries[0] ? new Date(groupedEntries[0].record.occurred_at).getTime() : 0;
  const matchingIndexes = useMemo(() => new Set(groupedEntries
    .map((entry, index) => timelineEntryMatches(entry, parsedQuery, timelineStart) ? index : -1)
    .filter((index) => index >= 0)), [groupedEntries, parsedQuery, timelineStart]);
  const visibleIndexes = useMemo(
    () => queryActive ? indexesForContext(groupedEntries.length, matchingIndexes, contextMode) : indexesForContext(groupedEntries.length, matchingIndexes, "full"),
    [contextMode, groupedEntries.length, matchingIndexes, queryActive],
  );
  const visibleEntries = useMemo(() => groupedEntries.filter((_, index) => visibleIndexes.has(index)), [groupedEntries, visibleIndexes]);
  const matchedIDs = useMemo(() => new Set([...matchingIndexes].map((index) => groupedEntries[index]?.record.id).filter(Boolean)), [groupedEntries, matchingIndexes]);
  const analysis = useMemo(() => analyzeSession(entries), [entries]);
  const selected = visibleEntries.find((entry) => entry.record.id === selectedID)
    ?? visibleEntries.find((entry) => entry.record.kind === "message.user")
    ?? visibleEntries[0]
    ?? null;

  useEffect(() => {
    setSelectedID(null);
    setDetailTab("rendered");
  }, [conversation?.id]);

  useEffect(() => {
    const url = new URL(window.location.href);
    if (timelineQuery.trim()) url.searchParams.set("timeline", timelineQuery.trim());
    else url.searchParams.delete("timeline");
    if (contextMode !== "context") url.searchParams.set("timeline_context", contextMode);
    else url.searchParams.delete("timeline_context");
    window.history.replaceState(null, "", url);
  }, [contextMode, timelineQuery]);

  const observedFields = useMemo(() => {
    const tagFields = new Set(groupedEntries.flatMap((entry) => Object.keys(entry.record.tags).map((key) => `tag.${key}`)));
    return [...filterFields, ...tagFields];
  }, [groupedEntries]);
  const suggestions = useMemo(() => {
    const current = timelineQuery.match(/(?:^|\s)([^\s]*)$/)?.[1] ?? "";
    const normalized = current.startsWith("-") ? current.slice(1) : current;
    const separator = normalized.indexOf(":");
    if (separator < 0) {
      const fragment = normalized.toLowerCase();
      return observedFields.filter((field) => field.includes(fragment)).slice(0, 7).map((field) => ({ value: `${current.startsWith("-") ? "-" : ""}${field}:`, label: `${field}:`, hint: "field" }));
    }
    const field = normalized.slice(0, separator);
    const partial = normalized.slice(separator + 1).replace(/^"|"$/g, "").toLowerCase();
    const fixed = field === "has" ? ["trace", "tags", "tool", "agent", "status"]
      : field === "offset" ? [">1s", ">5s", ">30s", ">1m"]
      : field === "size" ? [">1kb", ">10kb", ">1mb"]
      : field === "tokens" ? [">1k", ">10k", ">100k"]
      : observedFilterValues(groupedEntries, field);
    return fixed.filter((value) => value.toLowerCase().includes(partial)).slice(0, 7).map((value) => ({
      value: `${current.startsWith("-") ? "-" : ""}${field}:${quoteFilterValue(value)}`,
      label: value,
      hint: field,
    }));
  }, [groupedEntries, observedFields, timelineQuery]);
  const facetCounts = useMemo(() => facetDefinitions.map((facet) => {
    const parsed = parseTimelineQuery(facet.query);
    return groupedEntries.filter((entry) => timelineEntryMatches(entry, parsed, timelineStart)).length;
  }), [groupedEntries, timelineStart]);

  const applySuggestion = (value: string) => {
    setTimelineQuery((current) => `${current.replace(/(?:^|\s)[^\s]*$/, "").trim()}${current.replace(/(?:^|\s)[^\s]*$/, "").trim() ? " " : ""}${value}${value.endsWith(":") ? "" : " "}`);
    setSuggestionIndex(0);
  };

  const addFilter = (field: FilterField, value: string) => {
    const token = `${field}:${quoteFilterValue(value)}`;
    setTimelineQuery((current) => current.split(/\s+/).includes(token) ? current : appendQueryToken(current, token));
  };

  if (!conversation) return <InspectorEmpty />;
  if (loading) return <InspectorLoading conversation={conversation} />;
  if (error) return <InspectorError message={error} onRetry={onRetry} />;

  const statusTone = analysis.status === "complete" ? "success" : analysis.status === "failed" ? "danger" : analysis.status === "running" ? "warning" : "info";

  const exportSession = () => {
    const blob = new Blob([JSON.stringify({ conversation, analysis, timeline: entries }, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `${conversation.id}.json`;
    anchor.click();
    URL.revokeObjectURL(url);
  };

  return (
    <main className="flex min-w-0 flex-1 flex-col overflow-hidden">
      <header className="shrink-0 border-b border-stroke bg-canvas px-4 pb-3 pt-3">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0 max-w-4xl">
            <div className="mb-1.5 flex flex-wrap items-center gap-1.5">
              <Badge tone={statusTone}>
                {analysis.status === "complete" ? <CheckCircle2 size={11} /> : analysis.status === "failed" ? <XCircle size={11} /> : <CircleDot size={11} />}
                {analysis.status}
              </Badge>
              <span className="font-mono text-[9px] text-ink-faint">session/{shortID(conversation.id, 14)}</span>
              <button
                type="button"
                onClick={() => navigator.clipboard.writeText(conversation.id)}
                className="text-ink-faint transition-colors hover:text-ink-muted"
                aria-label="Copy session ID"
              >
                <Copy size={11} />
              </button>
            </div>
            <h1 className="text-balance line-clamp-1 text-[14px] font-medium leading-5 tracking-[-.01em] text-ink" title={analysis.prompt ?? conversation.id}>
              {promptTitle(analysis.prompt, conversation)}
            </h1>
            <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-[9px] text-ink-faint">
              <span className="flex items-center gap-1"><Bot size={10} />{conversation.agents.join(", ") || "Unknown agent"}</span>
              <span className="flex items-center gap-1"><Clock3 size={10} />{formatTimestamp(conversation.first_seen_at)}</span>
              {analysis.models.map((model) => <Badge key={model} size="sm">{model}</Badge>)}
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-1">
            <Button variant="ghost" size="sm" onClick={exportSession}><FileJson2 size={14} />Export</Button>
            <Button variant="secondary" size="sm" onClick={onCompare}><Zap size={13} />Compare</Button>
          </div>
        </div>

        <div className="no-scrollbar mt-3 flex divide-x divide-stroke overflow-x-auto border-y border-stroke bg-surface/50">
          <Metric label="Duration" value={formatDuration(analysis.duration)} icon={<Gauge size={13} />} />
          <Metric label="Turns" value={formatNumber(analysis.turns)} icon={<Bot size={13} />} />
          <Metric label="Model calls" value={formatNumber(analysis.modelCalls)} icon={<Sparkles size={13} />} />
          <Metric label="Tool calls" value={formatNumber(analysis.toolCalls)} icon={<Wrench size={13} />} />
          <Metric
            label="Tokens"
            value={analysis.inputTokens + analysis.outputTokens ? formatNumber(analysis.inputTokens + analysis.outputTokens) : "—"}
            detail={analysis.inputTokens + analysis.outputTokens ? `${formatNumber(analysis.inputTokens)} in · ${formatNumber(analysis.outputTokens)} out` : "not reported"}
            icon={<CircleDot size={13} />}
          />
        </div>
      </header>

      <div className="grid min-h-0 flex-1 grid-cols-[minmax(270px,34%)_1fr] max-[1040px]:grid-cols-[minmax(250px,40%)_1fr] max-[720px]:grid-cols-1 max-[720px]:grid-rows-[1fr_1fr]">
        <section className="flex min-h-0 flex-col border-r border-stroke bg-surface max-[720px]:border-b max-[720px]:border-r-0" aria-label="Session timeline">
          <div className="shrink-0 border-b border-stroke px-2.5 pb-2 pt-2">
            <div className="flex items-center justify-between">
              <div>
                <h2 className="text-[10px] font-semibold uppercase tracking-[.07em] text-ink-muted">Timeline</h2>
                <p className="mt-0.5 font-mono text-[8px] tabular-nums text-ink-faint">{matchingIndexes.size} matched · {visibleEntries.length} shown · {entries.length} records</p>
              </div>
              <div className="flex rounded-[4px] border border-stroke bg-canvas p-px">
                {(["matches", "context", "full"] as const).map((mode) => (
                  <button
                    key={mode}
                    type="button"
                    onClick={() => setContextMode(mode)}
                    className={cn(
                      "rounded-[3px] px-1.5 py-0.5 text-[9px] capitalize transition-colors",
                      contextMode === mode ? "bg-surface-hover text-ink-secondary" : "text-ink-faint hover:text-ink-muted",
                    )}
                    title={mode === "matches" ? "Show matching records only" : mode === "context" ? "Include one surrounding record" : "Show the full trace and dim nonmatches"}
                  >
                    {contextLabels[mode]}
                  </button>
                ))}
              </div>
            </div>
            <div className="relative mt-1.5">
              <label className={cn("flex h-7 items-center gap-1.5 rounded-[4px] border bg-canvas px-2", parsedQuery.errors.length > 0 ? "border-danger/50" : "border-stroke focus-within:border-accent/70")}>
                <Search size={11} className="text-ink-faint" />
                <input
                  value={timelineQuery}
                  onChange={(event) => { setTimelineQuery(event.target.value); setSuggestionIndex(0); }}
                  onFocus={() => setQueryFocused(true)}
                  onBlur={() => window.setTimeout(() => setQueryFocused(false), 100)}
                  onKeyDown={(event) => {
                    if (event.key === "ArrowDown" && suggestions.length > 0) { event.preventDefault(); setSuggestionIndex((index) => (index + 1) % suggestions.length); }
                    if (event.key === "ArrowUp" && suggestions.length > 0) { event.preventDefault(); setSuggestionIndex((index) => (index - 1 + suggestions.length) % suggestions.length); }
                    if (event.key === "Enter" && queryFocused && suggestions[suggestionIndex]) { event.preventDefault(); applySuggestion(suggestions[suggestionIndex].value); }
                    if (event.key === "Escape") { setQueryFocused(false); event.currentTarget.blur(); }
                  }}
                  className="min-w-0 flex-1 bg-transparent font-mono text-[10px] text-ink outline-none placeholder:font-sans placeholder:text-ink-faint"
                  placeholder="Filter… kind:tool tool:web_search -status:error"
                  aria-label="Filter timeline"
                  role="combobox"
                  aria-expanded={queryFocused && suggestions.length > 0}
                  aria-controls="timeline-filter-suggestions"
                />
                {timelineQuery && <button type="button" className="text-ink-faint hover:text-ink-muted" onClick={() => setTimelineQuery("")} aria-label="Clear timeline filters"><XCircle size={11} /></button>}
              </label>
              {queryFocused && suggestions.length > 0 && (
                <div id="timeline-filter-suggestions" role="listbox" className="absolute inset-x-0 top-[30px] z-30 overflow-hidden rounded-[4px] border border-stroke-strong bg-surface-raised shadow-panel">
                  {suggestions.map((suggestion, index) => (
                    <button
                      key={`${suggestion.value}-${index}`}
                      type="button"
                      role="option"
                      aria-selected={index === suggestionIndex}
                      onMouseDown={(event) => event.preventDefault()}
                      onClick={() => applySuggestion(suggestion.value)}
                      className={cn("flex h-7 w-full items-center justify-between px-2.5 text-left font-mono text-[9px]", index === suggestionIndex ? "bg-surface-hover text-ink" : "text-ink-secondary hover:bg-surface-hover")}
                    >
                      <span className="truncate">{suggestion.label}</span><span className="ml-3 font-sans text-[8px] text-ink-faint">{suggestion.hint}</span>
                    </button>
                  ))}
                </div>
              )}
            </div>
            <div className="mt-1.5 flex min-w-0 items-center gap-1 overflow-x-auto no-scrollbar" aria-label="Timeline facets">
              {facetDefinitions.map((facet, index) => {
                const active = timelineQuery.split(/\s+/).includes(facet.token);
                return (
                  <button
                    key={facet.label}
                    type="button"
                    onClick={() => setTimelineQuery((current) => active ? removeQueryToken(current, facet.token) : appendQueryToken(current, facet.token))}
                    className={cn("flex h-5 shrink-0 items-center gap-1 rounded-[3px] border px-1.5 text-[8px] transition-colors", active ? "border-accent/30 bg-accent-soft/60 text-accent" : "border-stroke bg-canvas text-ink-faint hover:text-ink-muted")}
                    aria-pressed={active}
                  >
                    {facet.label}<span className="font-mono opacity-70">{facetCounts[index]}</span>
                  </button>
                );
              })}
              {parsedQuery.errors[0] && <span className="ml-auto truncate text-[8px] text-danger" title={parsedQuery.errors.join(" ")}>{parsedQuery.errors[0]}</span>}
            </div>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto py-1">
            {visibleEntries.length === 0 && (
              <div className="px-5 py-12 text-center"><Search size={16} className="mx-auto text-ink-faint" /><p className="mt-2 text-[11px] text-ink-muted">No events match “{timelineQuery}”</p></div>
            )}
            {visibleEntries.map((entry, index) => {
              const category = eventCategory(entry.record.kind);
              const Icon = categoryMeta[category].icon;
              const isSelected = selected?.record.id === entry.record.id;
              const isMatch = matchedIDs.has(entry.record.id);
              const elapsed = new Date(entry.record.occurred_at).getTime() - new Date(entries[0].record.occurred_at).getTime();
              return (
                <button
                  key={`${entry.record.id}-${index}`}
                  type="button"
                  data-testid="timeline-entry"
                  data-match={isMatch ? "true" : "false"}
                  onClick={() => { setSelectedID(entry.record.id); setDetailTab("rendered"); }}
                  className={cn(
                    "group relative flex w-full gap-2 border-y border-transparent px-2.5 py-1.5 text-left transition-colors",
                    isSelected ? "border-stroke bg-surface-hover" : "hover:bg-surface-raised",
                    queryActive && contextMode === "full" && !isMatch && "opacity-35",
                  )}
                >
                  {isSelected && <span className="absolute inset-y-0 left-0 w-0.5 bg-accent" />}
                  <span className={cn("relative z-10 grid size-5 shrink-0 place-items-center", timelineIconClasses[category])}>
                    <Icon size={12} strokeWidth={1.7} />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center justify-between gap-2">
                      <span className="truncate text-[10px] font-medium text-ink-secondary group-hover:text-ink">{eventTitle(entry)}</span>
                      <span className="shrink-0 font-mono text-[8px] tabular-nums text-ink-faint">+{formatDuration(elapsed)}</span>
                    </span>
                    <span className="mt-0.5 block truncate text-[9px] leading-4 text-ink-faint">{previewForEntry(entry)}</span>
                    {entry.groupedCount && entry.groupedCount > 1 && <Badge size="sm" className="mt-1.5">{entry.groupedCount} chunks merged</Badge>}
                  </span>
                </button>
              );
            })}
          </div>
        </section>

        <section className="flex min-h-0 min-w-0 flex-col bg-canvas" aria-label="Event details">
          {selected ? (
            <>
              <div className="flex h-10 shrink-0 items-center justify-between gap-3 border-b border-stroke bg-surface px-2.5">
                <div className="flex min-w-0 items-center gap-2.5">
                  <span className="grid size-5 place-items-center text-ink-muted">{(() => { const Icon = categoryMeta[eventCategory(selected.record.kind)].icon; return <Icon size={12} />; })()}</span>
                  <div className="min-w-0">
                    <h2 className="truncate text-[10px] font-medium text-ink-secondary">{eventTitle(selected)}</h2>
                    <p className="truncate font-mono text-[8px] text-ink-faint">{selected.record.id}</p>
                  </div>
                  <div className="hidden min-w-0 items-center gap-1 min-[1120px]:flex">
                    <FilterToken field="kind" value={selected.record.kind} onAdd={addFilter} />
                    {selected.record.tool && <FilterToken field="tool" value={selected.record.tool} onAdd={addFilter} />}
                    {selected.record.agent && <FilterToken field="agent" value={selected.record.agent} onAdd={addFilter} />}
                  </div>
                </div>
                <div className="flex items-center gap-1">
                  <div className="mr-1 flex rounded-[4px] border border-stroke bg-canvas p-px">
                    {(["rendered", "json"] as const).map((tab) => (
                      <button
                        key={tab}
                        type="button"
                        onClick={() => setDetailTab(tab)}
                        className={cn("rounded-[3px] px-1.5 py-0.5 text-[9px] capitalize", detailTab === tab ? "bg-surface-hover text-ink-secondary" : "text-ink-faint hover:text-ink-muted")}
                      >
                        {tab === "rendered" ? "Pretty" : "JSON"}
                      </button>
                    ))}
                  </div>
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label="Copy event"
                    onClick={async () => {
                      await navigator.clipboard.writeText(JSON.stringify({ record: selected.record, content: selected.content }, null, 2));
                      setCopied(true);
                      window.setTimeout(() => setCopied(false), 1200);
                    }}
                  >
                    {copied ? <Check size={14} className="text-success" /> : <Copy size={14} />}
                  </Button>
                </div>
              </div>
              <div className="min-h-0 flex-1 overflow-y-auto">
                <EventDetail entry={selected} tab={detailTab} />
              </div>
            </>
          ) : <div className="grid h-full place-items-center text-xs text-ink-faint">Select an event to inspect it.</div>}
        </section>
      </div>
    </main>
  );
}

function Metric({ label, value, detail, icon }: { label: string; value: string; detail?: string; icon: ReactNode }) {
  return (
    <div className="flex h-8 min-w-0 flex-1 items-center gap-2 px-3 max-[720px]:min-w-[112px] max-[720px]:shrink-0">
      <span className="text-ink-faint">{icon}</span>
      <div className="min-w-0">
        <div className="flex items-baseline gap-1.5">
          <span className="text-[9px] text-ink-faint">{label}</span>
          <span className="font-mono text-[10px] font-medium tabular-nums text-ink-secondary">{value}</span>
        </div>
        {detail && <span className="block truncate font-mono text-[7px] tabular-nums text-ink-faint">{detail}</span>}
      </div>
    </div>
  );
}

function FilterToken({ field, value, onAdd }: { field: FilterField; value: string; onAdd: (field: FilterField, value: string) => void }) {
  return (
    <button
      type="button"
      onClick={() => onAdd(field, value)}
      className="max-w-28 truncate rounded-[3px] border border-stroke bg-canvas px-1.5 py-0.5 font-mono text-[8px] text-ink-faint hover:border-stroke-strong hover:text-ink-muted"
      title={`Filter by ${field}:${value}`}
    >
      {field}:{value}
    </button>
  );
}

function EventDetail({ entry, tab }: { entry: TimelineEntry; tab: DetailTab }) {
  const category = eventCategory(entry.record.kind);
  if (tab === "json") {
    return <pre className="m-3 overflow-x-auto whitespace-pre-wrap break-words rounded-[4px] border border-stroke bg-[#0b0c0e] p-3 font-mono text-[10px] leading-5 text-ink-secondary">{JSON.stringify({ record: entry.record, content: entry.content }, null, 2)}</pre>;
  }

  return (
    <div className="p-3">
      <PrettyContent entry={entry} />
      <div className="mt-4 border-t border-stroke pt-3">
        <h3 className="mb-2 text-[9px] font-semibold uppercase tracking-[.08em] text-ink-faint">Evidence metadata</h3>
        <div className="grid grid-cols-2 gap-px overflow-hidden rounded-[4px] border border-stroke bg-stroke max-[1080px]:grid-cols-1">
          <MetaItem icon={<Clock3 size={13} />} label="Occurred" value={formatTimestamp(entry.record.occurred_at)} />
          <MetaItem icon={<Fingerprint size={13} />} label="Trace" value={entry.record.trace_id ? shortID(entry.record.trace_id, 18) : "Not linked"} mono />
          <MetaItem icon={<Database size={13} />} label="Storage" value={`${entry.record.content.storage ?? "blob"} · ${formatNumber(entry.record.content.size)} bytes`} />
          <MetaItem icon={<CheckCircle2 size={13} />} label="Integrity" value={entry.record.record_sha256 ? "SHA-256 sealed" : "Content addressed"} success />
        </div>
        {Object.keys(entry.record.tags).length > 0 && (
          <div className="mt-3">
            <p className="mb-2 text-[10px] text-ink-faint">Tags</p>
            <div className="flex flex-wrap gap-1.5">
              {Object.entries(entry.record.tags).map(([key, value]) => <Badge key={key}>{key}: {value}</Badge>)}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function PrettyContent({ entry }: { entry: TimelineEntry }) {
  const content = asObject(entry.content);
  if (!content) return <CodeBlock value={entry.content} />;

  if ((entry.record.kind === "message.user" || entry.record.kind === "message.assistant") && typeof content.text === "string") {
    return (
      <Panel className="overflow-hidden" elevation="raised">
        <div className="flex h-8 items-center gap-2 border-b border-stroke bg-surface px-3">
          {entry.record.kind === "message.user" ? <UserRound size={12} className="text-ink-muted" /> : <Bot size={12} className="text-ink-muted" />}
          <span className="text-[9px] font-semibold uppercase tracking-[.08em] text-ink-muted">{entry.record.kind === "message.user" ? "Input" : "Output"}</span>
        </div>
        <div className="markdown-body p-3.5"><ReactMarkdown>{content.text}</ReactMarkdown></div>
      </Panel>
    );
  }

  if (entry.record.kind === "tool.call") {
    const input = asObject(content.input);
    return (
      <Panel className="overflow-hidden" elevation="raised">
        <div className="flex h-8 items-center justify-between border-b border-stroke bg-surface px-3">
          <div className="flex items-center gap-2 text-ink-muted"><Wrench size={12} /><span className="text-[9px] font-semibold uppercase tracking-[.08em]">Tool input</span></div>
          <Badge tone="success">{entry.record.tool ?? String(content.name ?? "tool")}</Badge>
        </div>
        {typeof input?.query === "string" ? (
          <div className="flex gap-2.5 p-3.5"><Search size={13} className="mt-0.5 shrink-0 text-ink-faint" /><p className="text-xs leading-5 text-ink-secondary">{input.query}</p></div>
        ) : <CodeBlock value={content.input ?? content} inset />}
      </Panel>
    );
  }

  if (entry.record.kind === "tool.result" && Array.isArray(content.content)) {
    const results = content.content.map(asObject).filter((value): value is Record<string, JSONValue> => Boolean(value));
    return (
      <div>
        <div className="mb-2.5 flex items-center justify-between"><p className="text-[10px] font-semibold uppercase tracking-[.08em] text-ink-faint">Returned evidence</p><Badge tone="success">{results.length} results</Badge></div>
        <div className="space-y-2">
          {results.slice(0, 8).map((result, index) => (
            <a
              key={`${String(result.url)}-${index}`}
              href={typeof result.url === "string" ? result.url : undefined}
              target="_blank"
              rel="noreferrer"
              className="group block rounded-[4px] border border-stroke bg-surface-raised p-2.5 transition-colors hover:border-stroke-strong hover:bg-surface-hover"
            >
              <div className="flex items-start justify-between gap-3">
                <p className="text-xs font-medium leading-5 text-ink-secondary group-hover:text-ink">{typeof result.title === "string" ? result.title : `Result ${index + 1}`}</p>
                <ExternalLink size={12} className="mt-1 shrink-0 text-ink-faint" />
              </div>
              {typeof result.url === "string" && <p className="mt-1 truncate text-[10px] text-ink-faint">{result.url}</p>}
              {typeof result.page_age === "string" && <Badge size="sm" className="mt-2">{result.page_age}</Badge>}
            </a>
          ))}
          {results.length > 8 && <p className="py-2 text-center text-[10px] text-ink-faint">+ {results.length - 8} additional results in JSON</p>}
        </div>
      </div>
    );
  }

  if (entry.record.kind === "model.request") {
    return (
      <Panel className="overflow-hidden" elevation="raised">
        <div className="flex h-8 items-center justify-between border-b border-stroke bg-surface px-3">
          <div className="flex items-center gap-2 text-ink-muted"><Sparkles size={12} /><span className="text-[9px] font-semibold uppercase tracking-[.08em]">Generation request</span></div>
          {typeof content.provider === "string" && <Badge tone="warning">{content.provider}</Badge>}
        </div>
        <div className="grid grid-cols-2 gap-px bg-stroke">
          <ValueCell label="Model" value={content.model} />
          <ValueCell label="Max output" value={typeof content.max_tokens === "number" ? `${formatNumber(content.max_tokens)} tokens` : undefined} />
          <ValueCell label="Messages" value={content.message_count} />
          <ValueCell label="Tools available" value={Array.isArray(content.tools) ? content.tools.length : 0} />
        </div>
      </Panel>
    );
  }

  if (entry.record.kind === "agent.turn.completed") {
    const usage = asObject(content.usage);
    return (
      <Panel className="overflow-hidden" elevation="raised">
        <div className="flex h-8 items-center justify-between border-b border-stroke bg-surface px-3">
          <div className="flex items-center gap-2 text-ink-muted"><CheckCircle2 size={12} /><span className="text-[9px] font-semibold uppercase tracking-[.08em]">Turn complete</span></div>
          {typeof content.stop_reason === "string" && <Badge tone={content.stop_reason === "end_turn" ? "success" : "warning"}>{content.stop_reason}</Badge>}
        </div>
        <div className="grid grid-cols-2 gap-px bg-stroke">
          <ValueCell label="Input tokens" value={usage?.input_tokens} />
          <ValueCell label="Output tokens" value={usage?.output_tokens} />
          <ValueCell label="Cache read" value={usage?.cache_read_input_tokens} />
          <ValueCell label="Service tier" value={usage?.service_tier} />
        </div>
      </Panel>
    );
  }

  return <CodeBlock value={entry.content} />;
}

function ValueCell({ label, value }: { label: string; value: JSONValue | undefined }) {
  const display = value === undefined || value === null ? "—" : typeof value === "object" ? JSON.stringify(value) : String(value);
  return <div className="min-w-0 bg-surface-raised px-3 py-2.5"><p className="text-[8px] uppercase tracking-[.08em] text-ink-faint">{label}</p><p className="mt-1 truncate font-mono text-[10px] tabular-nums text-ink-secondary">{display}</p></div>;
}

function MetaItem({ icon, label, value, mono, success }: { icon: ReactNode; label: string; value: string; mono?: boolean; success?: boolean }) {
  return (
    <div className="flex min-w-0 items-center gap-2 bg-surface-raised px-3 py-2">
      <span className={success ? "text-success" : "text-ink-faint"}>{icon}</span>
      <div className="min-w-0"><p className="text-[9px] text-ink-faint">{label}</p><p className={cn("mt-0.5 truncate text-[10px] text-ink-secondary", mono && "font-mono")}>{value}</p></div>
    </div>
  );
}

function CodeBlock({ value, inset = false }: { value: JSONValue; inset?: boolean }) {
  return <pre className={cn("overflow-x-auto whitespace-pre-wrap break-words bg-[#0b0c0e] p-3 font-mono text-[10px] leading-5 text-ink-secondary", !inset && "rounded-[4px] border border-stroke")}>{JSON.stringify(value, null, 2)}</pre>;
}

function InspectorEmpty() {
  return <main className="hairline-grid grid min-w-0 flex-1 place-items-center"><div className="text-center"><Box size={26} className="mx-auto text-ink-faint" /><p className="mt-3 text-sm font-medium text-ink-secondary">Select a session</p><p className="mt-1 text-xs text-ink-faint">Inspect its execution timeline and immutable evidence.</p></div></main>;
}

function InspectorLoading({ conversation }: { conversation: ConversationSummary }) {
  return (
    <main className="min-w-0 flex-1 overflow-hidden p-5">
      <div className="animate-pulse-soft">
        <div className="h-4 w-28 rounded bg-surface-hover" /><div className="mt-4 h-6 w-3/5 rounded bg-surface-hover" /><div className="mt-3 h-3 w-2/5 rounded bg-surface-muted" />
        <div className="mt-6 h-12 rounded-[4px] border border-stroke bg-surface-raised" />
        <div className="mt-3 grid grid-cols-[34%_1fr] gap-3"><div className="h-[520px] rounded-[4px] border border-stroke bg-surface-raised" /><div className="h-[520px] rounded-[4px] border border-stroke bg-surface-raised" /></div>
      </div>
      <span className="sr-only">Loading {conversation.id}</span>
    </main>
  );
}

function InspectorError({ message, onRetry }: { message: string; onRetry: () => void }) {
  return <main className="hairline-grid grid min-w-0 flex-1 place-items-center"><Panel className="max-w-sm p-5 text-center" elevation="raised"><XCircle size={24} className="mx-auto text-danger" /><p className="mt-3 text-sm font-medium text-ink">Couldn’t open this session</p><p className="mt-1 text-xs leading-5 text-ink-muted">{message}</p><Button className="mt-4" size="sm" onClick={onRetry}>Try again</Button></Panel></main>;
}
