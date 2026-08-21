import { forwardRef, type ButtonHTMLAttributes, type HTMLAttributes } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "../lib/utils";

const segmentedControlVariants = cva(
  "inline-flex min-w-0 items-center rounded-[5px] border border-stroke bg-canvas p-px",
  {
    variants: {
      width: { auto: "", full: "w-full" },
    },
    defaultVariants: { width: "auto" },
  },
);

const segmentedItemVariants = cva(
  "inline-flex min-w-0 items-center justify-center gap-1.5 rounded-[3px] font-medium outline-none transition-[background,color,box-shadow] duration-100 focus-visible:ring-1 focus-visible:ring-accent disabled:pointer-events-none disabled:opacity-45",
  {
    variants: {
      active: {
        true: "bg-surface-hover text-ink shadow-[inset_0_0_0_1px_rgba(255,255,255,.025)]",
        false: "text-ink-faint hover:bg-surface-raised hover:text-ink-muted",
      },
      size: {
        xs: "h-5 px-1.5 text-[9px]",
        sm: "h-6 px-2 text-[10px]",
        md: "h-7 px-2.5 text-[11px]",
      },
      grow: { true: "flex-1", false: "" },
    },
    defaultVariants: { active: false, size: "sm", grow: false },
  },
);

type SegmentedControlProps = HTMLAttributes<HTMLDivElement> & VariantProps<typeof segmentedControlVariants>;

export function SegmentedControl({ className, width, ...props }: SegmentedControlProps) {
  return <div className={cn(segmentedControlVariants({ width }), className)} role="group" {...props} />;
}

type SegmentedControlItemProps = ButtonHTMLAttributes<HTMLButtonElement> & VariantProps<typeof segmentedItemVariants>;

export const SegmentedControlItem = forwardRef<HTMLButtonElement, SegmentedControlItemProps>(function SegmentedControlItem(
  { active, className, grow, size, type = "button", ...props },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      className={cn(segmentedItemVariants({ active, grow, size }), className)}
      aria-pressed={Boolean(active)}
      {...props}
    />
  );
});
