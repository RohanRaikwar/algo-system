import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"

/*
 * Alert / status blocks. role defaults to "alert"; pass role="status" (or
 * role={undefined}) where the original element had another role. Each
 * variant is complete; `tone` picks the colour family. AlertTitle /
 * AlertDescription / AlertAction are shadcn's and only fit the default variant.
 */
const alertVariants = cva("", {
  variants: {
    variant: {
      /** ReconnectBanner: red gradient strip, white semibold text (position it with fixed top-0 left-0 right-0 z-[200]). */
      banner:
        "flex items-center justify-center gap-[8px] py-[10px] px-[20px] bg-[linear-gradient(90deg,rgba(239,68,68,0.95),rgba(220,38,38,0.95))] text-white text-center text-[0.85rem] font-semibold tracking-[0.3px] [backdrop-filter:blur(8px)]",
      /** SystemHealth summary banner: surface card with a 3px tone-coloured left border; tone colours the leading svg. */
      status:
        "flex items-start gap-[12px] py-[14px] px-[16px] bg-sg-surface border border-solid border-sg-line border-l-[3px] rounded-[12px] [&>svg]:shrink-0 [&>svg]:mt-[1px]",
      /** TradingSettingsTab entry state ("New entries blocked / allowed"): soft tone fill, ctrl radius. */
      soft: "flex items-center gap-[8px] py-[10px] px-[12px] rounded-ctrl text-[0.88rem] font-medium",
      /** TradingSettingsTab error / "Saved at" line: icon + text in the tone colour, no box. */
      inline: "flex items-center gap-[8px] text-[0.84rem]",
      /** shadcn alerts, kept for generic use. */
      default:
        "group/alert relative grid w-full gap-0.5 rounded-lg border px-2.5 py-2 text-left text-sm has-[>svg]:grid-cols-[auto_1fr] has-[>svg]:gap-x-2 *:[svg]:row-span-2 *:[svg]:translate-y-0.5 *:[svg]:text-current *:[svg:not([class*='size-'])]:size-4 bg-card text-card-foreground",
      destructive:
        "group/alert relative grid w-full gap-0.5 rounded-lg border px-2.5 py-2 text-left text-sm has-[>svg]:grid-cols-[auto_1fr] has-[>svg]:gap-x-2 *:[svg]:row-span-2 *:[svg]:translate-y-0.5 *:[svg]:text-current *:[svg:not([class*='size-'])]:size-4 bg-card text-destructive",
    },
    /** profit / loss / warn / muted. */
    tone: {
      profit: "",
      loss: "",
      warn: "",
      muted: "",
    },
  },
  compoundVariants: [
    { variant: "status", tone: "profit", class: "border-l-sg-profit [&>svg]:text-sg-profit" },
    { variant: "status", tone: "warn", class: "border-l-sg-warn [&>svg]:text-sg-warn" },
    { variant: "status", tone: "loss", class: "border-l-sg-loss [&>svg]:text-sg-loss" },
    { variant: "soft", tone: "profit", class: "text-sg-profit bg-sg-profit-soft" },
    { variant: "soft", tone: "loss", class: "text-sg-loss bg-sg-loss-soft" },
    { variant: "soft", tone: "warn", class: "text-sg-warn bg-sg-warn-soft" },
    { variant: "inline", tone: "profit", class: "text-sg-profit" },
    { variant: "inline", tone: "loss", class: "text-sg-loss" },
    { variant: "inline", tone: "warn", class: "text-sg-warn" },
    { variant: "inline", tone: "muted", class: "text-sg-muted" },
  ],
  defaultVariants: {
    variant: "default",
  },
})

function Alert({
  className,
  variant,
  tone,
  ...props
}: React.ComponentProps<"div"> & VariantProps<typeof alertVariants>) {
  return (
    <div
      data-slot="alert"
      role="alert"
      className={cn(alertVariants({ variant, tone }), className)}
      {...props}
    />
  )
}

function AlertTitle({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-title"
      className={cn(
        "font-medium group-has-[>svg]/alert:col-start-2 [&_a]:underline [&_a]:underline-offset-3 [&_a]:hover:text-foreground",
        className
      )}
      {...props}
    />
  )
}

function AlertDescription({
  className,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-description"
      className={cn(
        "text-sm text-balance text-muted-foreground md:text-pretty [&_a]:underline [&_a]:underline-offset-3 [&_a]:hover:text-foreground [&_p:not(:last-child)]:mb-4",
        className
      )}
      {...props}
    />
  )
}

function AlertAction({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-action"
      className={cn("absolute top-2 right-2", className)}
      {...props}
    />
  )
}

export { Alert, AlertTitle, AlertDescription, AlertAction, alertVariants }
