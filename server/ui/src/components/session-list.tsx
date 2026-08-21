import { Search } from "lucide-react";
import { useMemo, useRef, useState } from "react";
import type { ConversationSummary } from "../lib/history";
import { cn, formatDuration, formatNumber, formatRelativeDate, shortID, titleFromID } from "../lib/utils";

type FilterKind = "all" | "agent" | "test";

type SessionListProps = {
  conversations: ConversationSummary[];
  selectedID: string | null;
  onSelect: (conversation: ConversationSummary) => void;
  loading: boolean;
};

function summaryStatus(conversation: ConversationSummary) {
  if (conversation.kinds.some((kind) => kind === "agent.turn.completed")) return "complete";
  if (conversation.kinds.some((kind) => kind.startsWith("message."))) return "captured";
  return "evidence";
}

export function SessionList({ conversations, selectedID, onSelect, loading }: SessionListProps) {
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<FilterKind>("all");
  const searchRef = useRef<HTMLInputElement>(null);

  const filtered = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    return conversations.filter((conversation) => {
      const typeMatches =
        filter === "all" ||
        (filter === "agent" && conversation.kinds.some((kind) => kind.startsWith("agent."))) ||
        (filter === "test" && conversation.kinds.some((kind) => kind.startsWith("test.")));
      const textMatches =
        !normalized ||
        conversation.id.toLowerCase().includes(normalized) ||
        conversation.agents.some((agent) => agent.toLowerCase().includes(normalized));
      return typeMatches && textMatches;
    });
  }, [conversations, filter, query]);

  return (
    <aside className="flex w-[282px] shrink-0 flex-col border-r border-stroke bg-surface max-[900px]:w-[260px] max-[720px]:hidden">
      <div className="border-b border-stroke px-2 pb-2 pt-2">
        <div className="mb-1.5 flex h-6 items-center justify-between px-1">
          <h2 className="text-[10px] font-semibold uppercase tracking-[.08em] text-ink-muted">Sessions</h2>
          <span className="font-mono text-[9px] tabular-nums text-ink-faint">{conversations.length}</span>
        </div>
        <label className="flex h-7 items-center gap-1.5 rounded-[4px] border border-stroke bg-canvas px-2 transition-colors focus-within:border-accent/70">
          <Search size={11} className="text-ink-faint" />
          <input
            ref={searchRef}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            className="min-w-0 flex-1 bg-transparent text-[10px] text-ink outline-none placeholder:text-ink-faint"
            placeholder="Filter sessions"
            aria-label="Search sessions"
          />
          <kbd className="rounded-[3px] border border-stroke px-1 py-px font-sans text-[8px] text-ink-faint">/</kbd>
        </label>
        <div className="mt-1.5 flex gap-0.5" role="group" aria-label="Session type">
          {(["all", "agent", "test"] as const).map((value) => (
            <button
              key={value}
              type="button"
              onClick={() => setFilter(value)}
              className={cn(
                "rounded-[3px] px-2 py-0.5 text-[9px] capitalize transition-colors",
                filter === value ? "bg-surface-hover text-ink-secondary" : "text-ink-faint hover:text-ink-muted",
              )}
            >
              {value}
            </button>
          ))}
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto py-1">
        {loading && <SessionListSkeleton />}
        {!loading && filtered.length === 0 && (
          <div className="grid place-items-center px-5 py-16 text-center">
            <div className="mb-3 grid size-8 place-items-center rounded-[4px] bg-surface-muted text-ink-faint"><Search size={14} /></div>
            <p className="text-xs font-medium text-ink-secondary">No matching sessions</p>
            <p className="mt-1 text-[11px] leading-5 text-ink-faint">Try a broader agent name or session ID.</p>
          </div>
        )}
        {filtered.map((conversation) => {
          const selected = conversation.id === selectedID;
          const duration = new Date(conversation.last_seen_at).getTime() - new Date(conversation.first_seen_at).getTime();
          const status = summaryStatus(conversation);
          const agent = conversation.agents[0] ?? titleFromID(conversation.id);
          return (
            <button
              key={conversation.id}
              type="button"
              onClick={() => onSelect(conversation)}
              className={cn(
                "group relative w-full border-y border-transparent px-2.5 py-2 text-left transition-colors",
                selected
                  ? "border-stroke bg-surface-hover"
                  : "hover:bg-surface-raised",
              )}
            >
              {selected && <span className="absolute inset-y-0 left-0 w-0.5 bg-accent" />}
              <div className="flex items-start gap-2">
                <span className={cn("mt-1 size-1.5 shrink-0 rounded-full", status === "complete" ? "bg-success" : status === "captured" ? "bg-ink-muted" : "bg-ink-faint")} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center justify-between gap-2">
                    <p className="truncate text-[11px] font-medium text-ink-secondary group-hover:text-ink">{agent.replaceAll("-", " ")}</p>
                    <span className="shrink-0 text-[9px] text-ink-faint">{formatRelativeDate(conversation.last_seen_at)}</span>
                  </div>
                  <div className="mt-1 flex items-center gap-1.5 font-mono text-[9px] text-ink-faint">
                    <span className="truncate">{shortID(conversation.id, 12)}</span>
                    <span>·</span>
                    <span className="tabular-nums">{formatNumber(conversation.record_count)} ev</span>
                    <span>·</span>
                    <span className="tabular-nums">{formatDuration(duration)}</span>
                    {conversation.agents.length > 1 && <span className="ml-auto">+{conversation.agents.length - 1}</span>}
                  </div>
                </div>
              </div>
            </button>
          );
        })}
      </div>
      <div className="flex h-7 items-center justify-between border-t border-stroke px-2.5 font-mono text-[8px] text-ink-faint">
        <span>projection: immutable</span>
        <span className="size-1.5 rounded-full bg-success" />
      </div>
    </aside>
  );
}

function SessionListSkeleton() {
  return (
    <div>
      {[0, 1, 2, 3, 4].map((value) => (
        <div key={value} className="h-[58px] animate-pulse-soft border-b border-stroke bg-surface-raised p-2.5">
          <div className="h-2.5 w-2/3 rounded bg-surface-hover" />
          <div className="mt-3 h-2 w-1/3 rounded bg-surface-muted" />
          <div className="mt-4 h-2 w-1/2 rounded bg-surface-muted" />
        </div>
      ))}
    </div>
  );
}
