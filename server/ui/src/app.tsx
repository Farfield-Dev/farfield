import { Activity, ChevronDown, Database, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Badge } from "./design-system/badge";
import { Button } from "./design-system/button";
import { AppRail } from "./components/app-rail";
import { ActivityOverview } from "./components/activity-overview";
import { FarfieldMark } from "./components/logo";
import { SessionList } from "./components/session-list";
import { SessionComparison } from "./components/session-comparison";
import { TraceInspector } from "./components/trace-inspector";
import { isDemoMode } from "./lib/demo-data";
import { fetchConversations, fetchTimeline, type ConversationSummary, type TimelineEntry } from "./lib/history";
import { formatNumber } from "./lib/utils";

export default function App() {
  const demoMode = isDemoMode();
  const [conversations, setConversations] = useState<ConversationSummary[]>([]);
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [entries, setEntries] = useState<TimelineEntry[]>([]);
  const [listLoading, setListLoading] = useState(true);
  const [timelineLoading, setTimelineLoading] = useState(false);
  const [listError, setListError] = useState<string | null>(null);
  const [timelineError, setTimelineError] = useState<string | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);
  const [comparisonOpen, setComparisonOpen] = useState(false);

  const selected = conversations.find((conversation) => conversation.id === selectedID) ?? null;
  const totalRecords = useMemo(() => conversations.reduce((sum, conversation) => sum + conversation.record_count, 0), [conversations]);

  const loadConversations = useCallback(() => {
    const controller = new AbortController();
    setListLoading(true);
    setListError(null);
    fetchConversations(controller.signal)
      .then((values) => {
        setConversations(values);
        setSelectedID((current) => {
          if (current && values.some((value) => value.id === current)) return current;
          return values.reduce<ConversationSummary | null>(
            (best, value) => !best || value.record_count > best.record_count ? value : best,
            null,
          )?.id ?? null;
        });
      })
      .catch((error: unknown) => {
        if (error instanceof DOMException && error.name === "AbortError") return;
        setListError(error instanceof Error ? error.message : "Could not load history.");
      })
      .finally(() => setListLoading(false));
    return () => controller.abort();
  }, []);

  useEffect(loadConversations, [loadConversations, refreshKey]);

  useEffect(() => {
    if (!selectedID) { setEntries([]); return; }
    const controller = new AbortController();
    setTimelineLoading(true);
    setTimelineError(null);
    fetchTimeline(selectedID, controller.signal)
      .then(setEntries)
      .catch((error: unknown) => {
        if (error instanceof DOMException && error.name === "AbortError") return;
        setTimelineError(error instanceof Error ? error.message : "Could not load this session.");
      })
      .finally(() => setTimelineLoading(false));
    return () => controller.abort();
  }, [selectedID, refreshKey]);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "/" && !["INPUT", "TEXTAREA"].includes((event.target as HTMLElement).tagName)) {
        event.preventDefault();
        (document.querySelector<HTMLInputElement>('input[aria-label="Filter timeline"]')
          ?? document.querySelector<HTMLInputElement>('input[aria-label="Search sessions"]'))?.focus();
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, []);

  return (
    <div className="flex h-dvh min-h-[640px] overflow-hidden bg-canvas text-ink">
      <AppRail />
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-10 shrink-0 items-center justify-between border-b border-stroke bg-surface px-2.5">
          <div className="flex min-w-0 items-center gap-2.5">
            <FarfieldMark className="min-[901px]:hidden" />
            <button type="button" className="flex min-w-0 items-center gap-1.5 rounded-[4px] px-1.5 py-1 text-left hover:bg-surface-hover max-[720px]:hidden">
              <span className="truncate text-[11px] font-medium text-ink-secondary">farfield</span>
              <span className="text-ink-faint">/</span>
              <span className="truncate text-[11px] text-ink">history</span>
              <ChevronDown size={11} className="ml-0.5 shrink-0 text-ink-faint" />
            </button>
            <span className="h-3.5 w-px bg-stroke max-[720px]:hidden" />
            <select
              className="hidden h-7 w-[170px] shrink-0 rounded-[4px] border border-stroke bg-surface-raised px-2 text-[10px] text-ink outline-none max-[720px]:block"
              value={selectedID ?? ""}
              onChange={(event) => setSelectedID(event.target.value)}
              aria-label="Select session"
            >
              {conversations.map((conversation) => <option key={conversation.id} value={conversation.id}>{conversation.agents[0] ?? conversation.id} · {conversation.id.slice(-8)}</option>)}
            </select>
            <div className="flex items-center gap-1.5 text-[9px] text-ink-faint">
              <Database size={11} />
              <span className="max-[480px]:hidden">Object store</span>
              <span className="size-1.5 rounded-full bg-success" />
            </div>
            {demoMode && (
              <Badge tone="neutral" className="max-[560px]:hidden">Demo data</Badge>
            )}
          </div>
          <div className="flex items-center gap-1.5">
            {!listLoading && !listError && (
              <div className="mr-1 hidden items-center gap-2 text-[9px] text-ink-faint min-[760px]:flex">
                <Activity size={11} />
                <span>{formatNumber(conversations.length)} sessions</span>
                <span className="size-0.5 rounded-full bg-stroke-focus" />
                <span>{formatNumber(totalRecords)} records</span>
              </div>
            )}
            <Button variant="ghost" size="icon" aria-label="Refresh history" title="Refresh history" onClick={() => setRefreshKey((value) => value + 1)}>
              <RefreshCw size={14} className={listLoading || timelineLoading ? "animate-spin" : ""} />
            </Button>
            <span className="ml-0.5 grid size-6 place-items-center rounded-[4px] border border-stroke bg-surface-muted text-[8px] font-semibold text-ink-muted">FF</span>
          </div>
        </header>

        {!listError && <ActivityOverview conversations={conversations} loading={listLoading} />}

        {listError ? (
          <div className="grid min-h-0 flex-1 place-items-center p-6 text-center">
            <div>
              <Badge tone="danger">History unavailable</Badge>
              <p className="mt-3 text-sm font-medium text-ink">Farfield could not read the conversation projection.</p>
              <p className="mt-1 text-xs text-ink-muted">{listError}</p>
              <Button size="sm" className="mt-4" onClick={() => setRefreshKey((value) => value + 1)}>Retry</Button>
            </div>
          </div>
        ) : (
          <div className="flex min-h-0 flex-1">
            <SessionList
              conversations={conversations}
              selectedID={selectedID}
              onSelect={(conversation) => setSelectedID(conversation.id)}
              loading={listLoading}
            />
            <TraceInspector
              conversation={selected}
              entries={entries}
              loading={timelineLoading}
              error={timelineError}
              onRetry={() => setRefreshKey((value) => value + 1)}
              onCompare={() => setComparisonOpen(true)}
            />
          </div>
        )}
      </div>
      {comparisonOpen && selected && entries.length > 0 && (
        <SessionComparison
          base={selected}
          baseEntries={entries}
          conversations={conversations}
          onClose={() => setComparisonOpen(false)}
        />
      )}
    </div>
  );
}
