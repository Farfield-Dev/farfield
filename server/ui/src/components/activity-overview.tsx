import { cva } from "class-variance-authority";
import { Activity, Bot, CircleDot, Rows3 } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";
import { buildDailyActivity } from "../lib/activity";
import type { ConversationSummary } from "../lib/history";
import { cn, formatNumber } from "../lib/utils";

type Metric = "agents" | "sessions" | "events";

const metricButton = cva(
  "rounded-[3px] px-1.5 py-0.5 text-[9px] transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent",
  {
    variants: {
      selected: {
        true: "bg-surface-hover text-ink-secondary",
        false: "text-ink-faint hover:text-ink-muted",
      },
    },
  },
);

const metricLabels: Record<Metric, string> = {
  agents: "Agents",
  sessions: "Sessions",
  events: "Events",
};

export function ActivityOverview({ conversations, loading }: { conversations: ConversationSummary[]; loading: boolean }) {
  const [metric, setMetric] = useState<Metric>("agents");
  const activity = useMemo(() => buildDailyActivity(conversations, 14), [conversations]);
  const maximum = Math.max(1, ...activity.map((day) => day[metric]));
  const uniqueAgents = useMemo(() => new Set(conversations.flatMap((conversation) => conversation.agents)).size, [conversations]);
  const totalEvents = useMemo(() => conversations.reduce((sum, conversation) => sum + conversation.record_count, 0), [conversations]);
  const activeDays = activity.filter((day) => day.sessions > 0).length;
  const dateFormatter = useMemo(() => new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric" }), []);

  return (
    <section className="flex h-[112px] shrink-0 flex-col border-b border-stroke bg-canvas px-3 pb-2 pt-2 max-[720px]:h-[100px] max-[720px]:px-2.5" aria-label="Activity overview">
        <div className="flex h-5 items-center justify-between gap-3">
          <div className="flex min-w-0 items-center gap-2">
            <div className="flex shrink-0 items-center gap-1.5 text-[9px] font-semibold uppercase tracking-[.08em] text-ink-muted">
              <Activity size={11} />Activity
            </div>
            <span className="h-3 w-px bg-stroke" />
            <h2 className="shrink-0 text-[10px] font-medium text-ink-secondary">Daily {metricLabels[metric].toLowerCase()}</h2>
            <span className="truncate text-[8px] text-ink-faint">14 days · local time</span>
          </div>
          <div className="flex shrink-0 rounded-[4px] border border-stroke bg-surface p-px" role="group" aria-label="Activity metric">
            {(Object.keys(metricLabels) as Metric[]).map((value) => (
              <button key={value} type="button" className={metricButton({ selected: metric === value })} onClick={() => setMetric(value)}>
                {metricLabels[value]}
              </button>
            ))}
          </div>
        </div>

        <div className="mt-1.5 flex min-h-0 flex-1 gap-5">
          <div className="flex w-[282px] shrink-0 flex-col justify-center max-[900px]:w-[260px] max-[720px]:hidden">
            <div className="grid grid-cols-3 gap-4">
              <SummaryMetric icon={<Bot size={11} />} label="Agents" value={uniqueAgents} />
              <SummaryMetric icon={<Rows3 size={11} />} label="Sessions" value={conversations.length} />
              <SummaryMetric icon={<CircleDot size={11} />} label="Events" value={totalEvents} />
            </div>
            <p className="mt-1.5 font-mono text-[8px] text-ink-faint">{activeDays} active day{activeDays === 1 ? "" : "s"} · grouped by session start</p>
          </div>

          <div className={cn("flex min-h-0 flex-1 items-end gap-1 border-b border-stroke px-0.5", loading && "animate-pulse-soft")}>
            {activity.map((day, index) => {
              const value = day[metric];
              const height = value === 0 ? 1 : Math.max(5, (value / maximum) * 45);
              const showLabel = index === 0 || index === activity.length - 1 || index % 4 === 1;
              return (
                <div key={day.key} className="group relative flex h-full min-w-0 flex-1 items-end justify-center" title={`${dateFormatter.format(day.date)} · ${formatNumber(value)} ${metricLabels[metric].toLowerCase()}`}>
                  <span
                    className={cn("w-full max-w-7 border border-stroke-strong bg-ink-faint/45 transition-[height,background-color] duration-150 group-hover:bg-ink-muted/65", index === activity.length - 1 && value > 0 && "border-accent/40 bg-accent/55")}
                    style={{ height: `${height}px` }}
                    role="img"
                    aria-label={`${dateFormatter.format(day.date)}: ${formatNumber(value)} ${metricLabels[metric].toLowerCase()}`}
                  />
                  {showLabel && <span className="absolute -bottom-[13px] whitespace-nowrap font-mono text-[7px] text-ink-faint">{dateFormatter.format(day.date)}</span>}
                </div>
              );
            })}
          </div>
        </div>
    </section>
  );
}

function SummaryMetric({ icon, label, value }: { icon: ReactNode; label: string; value: number }) {
  return (
    <div className="min-w-0">
      <div className="flex items-center gap-1 text-[8px] text-ink-faint">{icon}{label}</div>
      <div className="mt-1 font-mono text-[13px] font-medium tabular-nums text-ink-secondary">{formatNumber(value)}</div>
    </div>
  );
}
