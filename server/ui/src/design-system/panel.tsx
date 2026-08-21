import { cva, type VariantProps } from "class-variance-authority";
import { type HTMLAttributes } from "react";
import { cn } from "../lib/utils";

const panelVariants = cva("border border-stroke bg-surface-raised", {
  variants: {
    radius: { none: "", md: "rounded-[5px]", lg: "rounded-md" },
    elevation: { flat: "", raised: "shadow-panel" },
  },
  defaultVariants: { radius: "md", elevation: "flat" },
});

type PanelProps = HTMLAttributes<HTMLDivElement> & VariantProps<typeof panelVariants>;

export function Panel({ className, radius, elevation, ...props }: PanelProps) {
  return <div className={cn(panelVariants({ radius, elevation }), className)} {...props} />;
}
