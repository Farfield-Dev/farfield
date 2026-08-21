import {
  ArrowRight,
  Bot,
  BrainCircuit,
  ChevronDown,
  ChevronRight,
  CircleDot,
  Clock3,
  MessageSquare,
  Sparkles,
  UserRound,
  Wrench,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import ReactMarkdown from "react-markdown";
import { Badge } from "../design-system/badge";
import { Button } from "../design-system/button";
import { buildReviewBlocks, type ReviewActivityBlock, type ReviewMessageBlock } from "../lib/conversation-review";
import { isReasoningEntry, previewForEntry, type JSONValue, type TimelineEntry } from "../lib/history";
import { cn, formatDuration, formatTimestamp, shortID } from "../lib/utils";

type ConversationReviewProps = {
  entries: TimelineEntry[];
  complete: boolean;
  onOpenEntry: (entry: TimelineEntry) => void;
};

function asObject(value: JSONValue): Record<string, JSONValue> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value : null;
}

function messageText(entry: TimelineEntry) {
  const content = asObject(entry.content);
  return typeof content?.text === "string" ? content.text : JSON.stringify(entry.content, null, 2);
}

export function ConversationReview({ entries, complete, onOpenEntry }: ConversationReviewProps) {
  const [expandedActivities, setExpandedActivities] = useState<Set<string>>(new Set());
  const [expandedMessages, setExpandedMessages] = useState<Set<string>>(new Set());
  const blocks = useMemo(() => buildReviewBlocks(entries), [entries]);
  const messages = blocks.filter((block): block is ReviewMessageBlock => block.type === "message");
  const latestAssistant = [...messages].reverse().find((block) => block.role === "assistant");
  const reasoningCount = entries.filter(isReasoningEntry).length;
  const visibleBlocks = blocks.filter((block) => block.type === "message" || block.entries.some((entry) => !entry.record.kind.startsWith("agent.turn.")));

  useEffect(() => {
    setExpandedActivities(new Set());
    setExpandedMessages(new Set());
  }, [entries]);

  const jumpToLatest = () => {
    if (!latestAssistant) return;
    document.getElementById(`review-${latestAssistant.id}`)?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  return (
    <section className="flex min-h-0 flex-1 flex-col bg-canvas" aria-label="Conversation review">
      <div className="flex min-h-11 shrink-0 items-center justify-between gap-3 border-b border-stroke bg-surface px-3">
        <div className="min-w-0">
          <h2 className="text-[10px] font-semibold uppercase tracking-[.07em] text-ink-muted">Conversation review</h2>
          <p className="mt-0.5 text-[8px] text-ink-faint">{messages.length} message{messages.length === 1 ? "" : "s"}{reasoningCount ? ` · ${reasoningCount} reasoning record${reasoningCount === 1 ? "" : "s"}` : ""} · captured content shown verbatim</p>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {latestAssistant && (
            <Button variant="ghost" size="sm" onClick={jumpToLatest} className="max-[860px]:hidden">
              Latest response <ArrowRight size={12} />
            </Button>
          )}
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto scroll-smooth px-4 py-5 max-[720px]:px-2.5">
        <div className="mx-auto max-w-[780px]">
          {visibleBlocks.map((block, index) => {
            if (block.type === "message") {
              return (
                <MessageCard
                  key={block.id}
                  block={block}
                  complete={complete}
                  expanded={expandedMessages.has(block.id)}
                  onToggle={() => setExpandedMessages((current) => {
                    const next = new Set(current);
                    if (next.has(block.id)) next.delete(block.id);
                    else next.add(block.id);
                    return next;
                  })}
                  onOpen={() => onOpenEntry(block.entry)}
                />
              );
            }
            return (
              <ActivityCard
                key={block.id}
                block={block}
                expanded={expandedActivities.has(block.id)}
                last={index === visibleBlocks.length - 1}
                onToggle={() => setExpandedActivities((current) => {
                  const next = new Set(current);
                  if (next.has(block.id)) next.delete(block.id);
                  else next.add(block.id);
                  return next;
                })}
                onOpenEntry={onOpenEntry}
              />
            );
          })}
          {messages.length === 0 && (
            <div className="rounded-[5px] border border-dashed border-stroke px-5 py-12 text-center">
              <MessageSquare size={18} className="mx-auto text-ink-faint" />
              <p className="mt-2 text-[11px] text-ink-muted">No conversation messages were captured.</p>
              <p className="mt-1 text-[9px] text-ink-faint">Open Trace to inspect the {entries.length} available records.</p>
            </div>
          )}
        </div>
      </div>
    </section>
  );
}

function MessageCard({ block, complete, expanded, onToggle, onOpen }: {
  block: ReviewMessageBlock;
  complete: boolean;
  expanded: boolean;
  onToggle: () => void;
  onOpen: () => void;
}) {
  const text = messageText(block.entry);
  const long = text.length > 1_800;
  const isUser = block.role === "user";
  const label = isUser ? "User prompt" : block.isLatestAssistant ? (complete ? "Final response" : "Latest agent response") : "Agent response";

  return (
    <article id={`review-${block.id}`} className={cn("scroll-mt-4 py-2.5", isUser && "mr-12 max-[640px]:mr-0")} data-testid="review-message">
      <div className="mb-2 flex items-center justify-between gap-3 px-0.5">
        <div className="flex min-w-0 items-center gap-2">
          <span className={cn("grid size-5 shrink-0 place-items-center rounded-[4px] border", isUser ? "border-accent/25 bg-accent-soft/60 text-accent" : "border-stroke bg-surface-muted text-ink-muted")}>{isUser ? <UserRound size={11} /> : <Bot size={11} />}</span>
          <span className={cn("text-[9px] font-semibold uppercase tracking-[.07em]", isUser ? "text-ink" : "text-ink-muted")}>{label}</span>
          {block.isLatestAssistant && <Badge size="sm">Latest</Badge>}
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <span className="font-mono text-[8px] tabular-nums text-ink-faint">{formatTimestamp(block.entry.record.occurred_at)}</span>
          <button type="button" onClick={onOpen} className="text-[8px] text-ink-faint hover:text-ink-muted" title="Open this exact record in Trace">View record</button>
        </div>
      </div>
      <div className={cn("relative overflow-hidden rounded-[6px] border shadow-[0_1px_0_rgba(255,255,255,.015)]", isUser ? "border-accent/20 border-l-2 border-l-accent/60 bg-accent-soft/20" : "border-stroke bg-surface-raised", long && !expanded && "max-h-[300px]")}>
        <div className={cn("markdown-body px-4 py-3.5", isUser ? "markdown-body-user" : "markdown-body-agent")}><ReactMarkdown>{text}</ReactMarkdown></div>
        {long && !expanded && <div className="pointer-events-none absolute inset-x-0 bottom-0 h-20 bg-gradient-to-t from-surface-raised to-transparent" />}
      </div>
      {long && (
        <button type="button" onClick={onToggle} className="mx-auto mt-1.5 flex items-center gap-1 px-2 py-1 text-[9px] text-ink-faint hover:text-ink-muted" aria-expanded={expanded}>
          {expanded ? "Collapse response" : "Show full response"}<ChevronDown size={11} className={cn("transition-transform", expanded && "rotate-180")} />
        </button>
      )}
    </article>
  );
}

function ActivityCard({ block, expanded, last, onToggle, onOpenEntry }: {
  block: ReviewActivityBlock;
  expanded: boolean;
  last: boolean;
  onToggle: () => void;
  onOpenEntry: (entry: TimelineEntry) => void;
}) {
  const start = block.entries[0];
  const summary = [
    block.toolCalls ? `${block.toolCalls} tool call${block.toolCalls === 1 ? "" : "s"}` : null,
    block.toolResults ? `${block.toolResults} result${block.toolResults === 1 ? "" : "s"}` : null,
    block.reasoningEvents ? `${block.reasoningEvents} reasoning` : null,
    block.modelEvents ? `${block.modelEvents} model event${block.modelEvents === 1 ? "" : "s"}` : null,
    block.errors ? `${block.errors} error${block.errors === 1 ? "" : "s"}` : null,
  ].filter(Boolean).join(" · ") || `${block.entries.length} record${block.entries.length === 1 ? "" : "s"}`;

  return (
    <div className={cn("relative ml-2.5 border-l pl-5", block.errors ? "border-danger/30" : "border-stroke", last ? "pb-1" : "py-1.5")} data-testid="review-activity">
      <span className={cn("absolute -left-[3px] top-5 size-[5px] rounded-full", block.errors ? "bg-danger" : "bg-stroke-focus")} />
      <div className={cn("overflow-hidden rounded-[5px] border", block.errors ? "border-danger/30 bg-danger/5" : "border-stroke bg-surface")}>
        <button type="button" onClick={onToggle} className="group flex w-full items-center gap-2.5 px-3 py-2 text-left hover:bg-surface-raised" aria-expanded={expanded}>
          <ChevronRight size={11} className={cn("shrink-0 transition-transform", block.errors ? "text-danger" : "text-ink-faint", expanded && "rotate-90")} />
          <span className="min-w-0 flex-1">
            <span className="flex items-center gap-2">
              <span className={cn("text-[9px] font-medium", block.errors ? "text-danger" : "text-ink-muted")}>{block.errors ? "Activity issue" : "Activity"}</span>
              <span className={cn("truncate text-[8px]", block.errors ? "text-danger/80" : "text-ink-faint")}>{summary}</span>
            </span>
            {block.tools.length > 0 && <span className="mt-0.5 block truncate font-mono text-[8px] text-ink-faint">{block.tools.join(" · ")}</span>}
          </span>
          <span className="flex shrink-0 items-center gap-1 font-mono text-[8px] tabular-nums text-ink-faint"><Clock3 size={10} />{block.duration ? formatDuration(block.duration) : "instant"}</span>
        </button>
        {expanded && (
          <div className="border-t border-stroke bg-canvas/40 py-1">
            {block.entries.map((entry) => {
              const Icon = isReasoningEntry(entry) ? BrainCircuit : entry.record.kind.startsWith("tool.") ? Wrench : entry.record.kind.startsWith("model.") ? Sparkles : CircleDot;
              const status = entry.record.status?.toLowerCase();
              const failed = status === "error" || status === "failed" || status === "cancelled";
              return (
                <button key={entry.record.id} type="button" onClick={() => onOpenEntry(entry)} className="group flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-surface-hover" title="Open exact record in Trace">
                  <Icon size={11} className={failed ? "text-danger" : "text-ink-faint"} />
                  <span className="w-32 shrink-0 truncate font-mono text-[8px] text-ink-muted">{entry.record.kind}{entry.record.tool ? ` · ${entry.record.tool}` : ""}</span>
                  <span className="min-w-0 flex-1 truncate text-[8px] text-ink-faint">{previewForEntry(entry)}</span>
                  <span className="shrink-0 font-mono text-[7px] text-ink-faint">{shortID(entry.record.id, 9)}</span>
                  <ArrowRight size={10} className="shrink-0 text-ink-faint opacity-0 group-hover:opacity-100" />
                </button>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
