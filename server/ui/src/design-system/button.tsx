import { forwardRef, type ButtonHTMLAttributes } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "../lib/utils";

export const buttonVariants = cva(
  "inline-flex shrink-0 items-center justify-center gap-1.5 rounded-[5px] font-medium outline-none transition-[background,color,border-color,box-shadow] duration-100 focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-offset-1 focus-visible:ring-offset-canvas disabled:pointer-events-none disabled:opacity-45",
  {
    variants: {
      variant: {
        primary: "border border-accent/70 bg-accent/90 text-accent-ink hover:bg-accent",
        secondary: "border border-stroke-strong bg-surface-raised text-ink-secondary hover:bg-surface-hover hover:text-ink",
        ghost: "text-ink-muted hover:bg-surface-hover hover:text-ink",
        quiet: "bg-surface-muted text-ink-muted hover:bg-surface-hover hover:text-ink",
        danger: "bg-danger-soft text-danger hover:bg-danger/15",
      },
      size: {
        sm: "h-7 px-2.5 text-[11px]",
        md: "h-8 px-3 text-xs",
        lg: "h-9 px-3.5 text-xs",
        icon: "size-7 p-0",
        "icon-lg": "size-8 p-0",
      },
    },
    defaultVariants: { variant: "secondary", size: "md" },
  },
);

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & VariantProps<typeof buttonVariants>;

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { className, variant, size, type = "button", ...props },
  ref,
) {
  return <button ref={ref} type={type} className={cn(buttonVariants({ variant, size }), className)} {...props} />;
});
