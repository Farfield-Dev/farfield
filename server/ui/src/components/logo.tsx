import { cn } from "../lib/utils";

export function FarfieldMark({ className }: { className?: string }) {
  return (
    <div className={cn("relative grid size-6 place-items-center overflow-hidden rounded-[4px] border border-stroke-strong bg-surface-raised text-ink", className)}>
      <svg viewBox="0 0 32 32" className="size-full" aria-hidden="true">
        <path d="M8 7h17v3H11v5h11v3H11v7H8V7Z" fill="currentColor" />
        <path d="M22 20h3v5h-3z" fill="currentColor" opacity=".38" />
      </svg>
    </div>
  );
}
