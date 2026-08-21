import { ArrowRight, Bot, CheckCircle2, Clock3, GitCompareArrows, LoaderCircle, Sparkles, Wrench, X } from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Badge } from "../design-system/badge";
import { Button } from "../design-system/button";
import { analyzeSession, fetchTimeline, type ConversationSummary, type TimelineEntry } from "../lib/history";
import { cn, formatDuration, formatNumber, shortID } from "../lib/utils";

type SessionComparisonProps = {
  base: ConversationSummary;
  baseEntries: TimelineEntry[];
  conversations: ConversationSummary[];
  onClose: () => void;
};

export function SessionComparison({ base, baseEntries, conversations, onClose }: SessionComparisonProps) {
  const candidates = conversations.filter((conversation) => conversation.id !== base.id);
  const preferred = candidates.find((conversation) => conversation.agents.some((agent) => base.agents.includes(agent))) ?? candidates[0];
  const [candidateID, setCandidateID] = useState(preferred?.id ?? "");
  const [candidateEntries, setCandidateEntries] = useState<TimelineEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const candidate = conversations.find((conversation) => conversation.id === candidateID) ?? null;
  const baseAnalysis = useMemo(() => analyzeSession(baseEntries), [baseEntries]);
  const candidateAnalysis = useMemo(() => analyzeSession(candidateEntries), [candidateEntries]);

  useEffect(() => {
    if (!candidateID) return;
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    fetchTimeline(candidateID, controller.signal)
      .then(setCandidateEntries)
      .catch((reason: unknown) => {
        if (reason instanceof DOMException && reason.name === "AbortError") return;
        setError(reason instanceof Error ? reason.message : "Could not load comparison session.");
      })
      .finally(() => setLoading(false));
    return () => controller.abort();
  }, [candidateID]);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => { if (event.key === "Escape") onClose(); };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [onClose]);

  const metrics = [
    { label: "Duration", icon: <Clock3 size={13} />, left: baseAnalysis.duration, right: candidateAnalysis.duration, format: formatDuration, lowerIsBetter: true },
    { label: "Events", icon: <Sparkles size={13} />, left: baseEntries.length, right: candidateEntries.length, format: formatNumber },
    { label: "Turns", icon: <Bot size={13} />, left: baseAnalysis.turns, right: candidateAnalysis.turns, format: formatNumber },
    { label: "Model calls", icon: <Sparkles size={13} />, left: baseAnalysis.modelCalls, right: candidateAnalysis.modelCalls, format: formatNumber },
    { label: "Tool calls", icon: <Wrench size={13} />, left: baseAnalysis.toolCalls, right: candidateAnalysis.toolCalls, format: formatNumber },
    { label: "Total tokens", icon: <CheckCircle2 size={13} />, left: baseAnalysis.inputTokens + baseAnalysis.outputTokens, right: candidateAnalysis.inputTokens + candidateAnalysis.outputTokens, format: formatNumber, lowerIsBetter: true },
  ];

  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-black/75 p-4" role="dialog" aria-modal="true" aria-label="Compare sessions">
      <div className="flex max-h-[88vh] w-full max-w-[820px] flex-col overflow-hidden rounded-md border border-stroke-strong bg-surface shadow-panel">
        <header className="flex items-start justify-between border-b border-stroke px-4 py-3">
          <div>
            <div className="flex items-center gap-1.5 text-ink-muted"><GitCompareArrows size={13} /><span className="text-[9px] font-semibold uppercase tracking-[.08em]">Session diff</span></div>
            <h2 className="mt-1 text-sm font-medium text-ink">Execution shape and resource use</h2>
            <p className="mt-0.5 text-[10px] text-ink-faint">Derived from immutable timeline records</p>
          </div>
          <Button variant="ghost" size="icon" onClick={onClose} aria-label="Close comparison"><X size={16} /></Button>
        </header>

        <div className="min-h-0 overflow-y-auto p-4">
          <div className="grid grid-cols-[1fr_auto_1fr] items-stretch gap-2">
            <SessionCard conversation={base} prompt={baseAnalysis.prompt} label="Current" />
            <div className="grid place-items-center px-1 text-ink-faint"><ArrowRight size={18} /></div>
            <div className="rounded-[4px] border border-stroke bg-surface-raised p-3">
              <div className="mb-2 flex items-center justify-between"><Badge tone="info">Compare with</Badge>{loading && <LoaderCircle size={13} className="animate-spin text-info" />}</div>
              <select
                value={candidateID}
                onChange={(event) => setCandidateID(event.target.value)}
                className="h-7 w-full rounded-[4px] border border-stroke-strong bg-canvas px-2 text-[10px] text-ink outline-none focus:border-accent/70"
                aria-label="Comparison session"
              >
                {candidates.map((conversation) => <option key={conversation.id} value={conversation.id}>{conversation.agents[0] ?? conversation.id} · {shortID(conversation.id, 10)}</option>)}
              </select>
              <p className="mt-2 line-clamp-2 min-h-8 text-[10px] leading-4 text-ink-muted">{candidateAnalysis.prompt ?? candidate?.id ?? "Choose another session"}</p>
              <p className="mt-2 font-mono text-[9px] text-ink-faint">session/{candidate ? shortID(candidate.id, 14) : "—"}</p>
            </div>
          </div>

          {error ? <div className="mt-4 rounded-xl border border-danger/20 bg-danger-soft p-4 text-xs text-danger">{error}</div> : (
            <div className={cn("mt-3 overflow-hidden rounded-[4px] border border-stroke transition-opacity", loading && "opacity-45")}>
              <div className="grid grid-cols-[1.1fr_1fr_1fr_.7fr] border-b border-stroke bg-surface-muted/60 px-3 py-1.5 text-[8px] font-semibold uppercase tracking-[.08em] text-ink-faint">
                <span>Metric</span><span>Current</span><span>Comparison</span><span className="text-right">Delta</span>
              </div>
              {metrics.map((metric) => {
                const delta = metric.right - metric.left;
                const percent = metric.left ? (delta / metric.left) * 100 : null;
                const improved = metric.lowerIsBetter ? delta < 0 : delta > 0;
                return (
                  <div key={metric.label} className="grid grid-cols-[1.1fr_1fr_1fr_.7fr] items-center border-b border-stroke px-3 py-2 last:border-0">
                    <span className="flex items-center gap-1.5 text-[10px] text-ink-muted">{metric.icon}{metric.label}</span>
                    <span className="font-mono text-[10px] font-medium tabular-nums text-ink-secondary">{metric.format(metric.left)}</span>
                    <span className="font-mono text-[10px] font-medium tabular-nums text-ink-secondary">{metric.format(metric.right)}</span>
                    <span className={cn("text-right text-[10px] font-medium tabular-nums", percent === null || delta === 0 ? "text-ink-faint" : improved ? "text-success" : "text-warning")}>
                      {percent === null ? "—" : delta === 0 ? "same" : `${delta > 0 ? "+" : ""}${percent.toFixed(0)}%`}
                    </span>
                  </div>
                );
              })}
            </div>
          )}

          <div className="mt-3 grid grid-cols-2 gap-2">
            <Insight label="Model mix" left={baseAnalysis.models.join(", ") || "Not reported"} right={candidateAnalysis.models.join(", ") || "Not reported"} />
            <Insight label="Execution traces" left={`${baseAnalysis.traces.length} trace${baseAnalysis.traces.length === 1 ? "" : "s"}`} right={`${candidateAnalysis.traces.length} trace${candidateAnalysis.traces.length === 1 ? "" : "s"}`} />
          </div>
        </div>
      </div>
    </div>
  );
}

function SessionCard({ conversation, prompt, label }: { conversation: ConversationSummary; prompt: string | null; label: string }) {
  return (
    <div className="rounded-[4px] border border-stroke bg-surface-raised p-3">
      <Badge>{label}</Badge>
      <p className="mt-2 line-clamp-2 min-h-8 text-[10px] leading-4 text-ink-secondary">{prompt ?? conversation.id}</p>
      <p className="mt-2 font-mono text-[9px] text-ink-faint">session/{shortID(conversation.id, 14)}</p>
    </div>
  );
}

function Insight({ label, left, right }: { label: string; left: string; right: string }) {
  return <div className="rounded-[4px] border border-stroke bg-surface-raised p-3"><p className="text-[8px] font-semibold uppercase tracking-[.08em] text-ink-faint">{label}</p><div className="mt-1.5 flex items-center gap-2 text-[9px] text-ink-secondary"><span className="min-w-0 flex-1 truncate">{left}</span><ArrowRight size={10} className="shrink-0 text-ink-faint" /><span className="min-w-0 flex-1 truncate text-right">{right}</span></div></div>;
}
