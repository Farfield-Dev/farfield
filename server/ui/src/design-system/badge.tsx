import { type HTMLAttributes } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "../lib/utils";

const badgeVariants = cva(
  "inline-flex min-w-0 items-center gap-1 rounded-[4px] border px-1.5 py-px text-[10px] font-medium leading-4",
  {
    variants: {
      tone: {
        neutral: "border-stroke bg-surface-muted text-ink-muted",
        success: "border-success/20 bg-success-soft/55 text-success",
        warning: "border-warning/20 bg-warning-soft/55 text-warning",
        danger: "border-danger/20 bg-danger-soft/55 text-danger",
        info: "border-stroke bg-surface-muted text-ink-muted",
        accent: "border-accent/25 bg-accent-soft/60 text-accent",
      },
      size: {
        sm: "px-1.5 py-0 text-[9px]",
        md: "px-1.5 py-px text-[10px]",
      },
    },
    defaultVariants: { tone: "neutral", size: "md" },
  },
);

type BadgeProps = HTMLAttributes<HTMLSpanElement> & VariantProps<typeof badgeVariants>;

export function Badge({ className, tone, size, ...props }: BadgeProps) {
  return <span className={cn(badgeVariants({ tone, size }), className)} {...props} />;
}
