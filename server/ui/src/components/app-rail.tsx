import { Database, History } from "lucide-react";
import { cn } from "../lib/utils";
import { FarfieldMark } from "./logo";

export function AppRail() {
  return (
    <aside className="desktop-rail flex w-11 shrink-0 flex-col items-center border-r border-stroke bg-surface py-2">
      <FarfieldMark className="mb-4" />
      <nav className="flex flex-1 flex-col gap-1" aria-label="Primary navigation">
        <button type="button" aria-label="History" title="History" className={cn("group relative grid size-8 place-items-center rounded-[4px] bg-surface-hover text-ink-secondary")}>
          <History size={16} strokeWidth={1.7} />
          <span className="absolute -left-[6px] h-5 w-0.5 bg-accent" />
        </button>
      </nav>
      <div className="mb-2 flex flex-col gap-1">
        <div
          role="status"
          aria-label="Object storage connected"
          title="Object storage connected"
          className="relative grid size-8 place-items-center rounded-[4px] text-ink-faint hover:bg-surface-hover hover:text-ink-muted"
        >
          <Database size={15} strokeWidth={1.7} />
          <span className="absolute bottom-1.5 right-1.5 size-1.5 rounded-full border border-surface bg-success" />
        </div>
      </div>
    </aside>
  );
}
