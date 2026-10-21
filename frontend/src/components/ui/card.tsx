import * as React from "react"
import { mergeProps } from "@base-ui/react/merge-props"
import { useRender } from "@base-ui/react/use-render"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"

/*
 * Card looks of the app. Each variant is one existing card shell, complete
 * (border, radius, background, padding where the original had it). The
 * shadcn card (ring, rounded-xl, --card-spacing gaps) is variant="classic",
 * the only one the CardHeader/CardContent/CardFooter spacing is made for.
 * Render as another element with render={<section />} / {<button />}.
 */
const cardVariants = cva("group/card", {
  variants: {
    variant: {
      /** `dayCard` / `fnoCard`: bordered bg card, 12px radius, clips its rows (day groups, F&O cards). */
      default: "border border-solid border-sg-line rounded-[12px] bg-sg-bg overflow-hidden",
      /** `summaryCard`: padded stat tile on bg (summaryCardShell + bg-sg-bg). */
      summary: "py-[12px] px-[14px] border border-solid border-sg-line rounded-[12px] min-w-0 bg-sg-bg",
      /** SessionHealthPanel `detailCard`: the same tile on surface. */
      "summary-surface": "py-[12px] px-[14px] border border-solid border-sg-line rounded-[12px] min-w-0 bg-sg-surface",
      /** TradingSettingsTab `card`: 18px padded settings card (14px on phones). tone="danger" when the kill switch is on. */
      settings: "p-[18px] bg-sg-bg border border-solid border-sg-line rounded-[12px] lte-640:p-[14px]",
      /** Phone `cardShell` row (signal / trade / More item); tap adds the pressed state. tone="accent" for a new signal. */
      row: "flex items-center gap-[12px] w-full min-h-[60px] py-[10px] px-[12px] border border-solid border-sg-line-soft rounded-[12px] bg-sg-surface text-sg-text leading-[inherit] text-left",
      /** SystemHealth `Card` section: 16px padded surface panel, panel radius. */
      panel: "flex flex-col p-[16px] bg-sg-surface border border-solid border-sg-line rounded-panel min-w-0",
      /** SRSheet `card`: borderless raised block. */
      sub: "py-[10px] px-[12px] rounded-ctrl bg-sg-raised",
      /** shadcn's card, kept for generic use. */
      classic:
        "flex flex-col gap-(--card-spacing) overflow-hidden rounded-xl bg-card py-(--card-spacing) text-sm text-card-foreground ring-1 ring-foreground/10 [--card-spacing:--spacing(4)] has-data-[slot=card-footer]:pb-0 has-[>img:first-child]:pt-0 data-[size=sm]:[--card-spacing:--spacing(3)] data-[size=sm]:has-data-[slot=card-footer]:pb-0 *:[img:first-child]:rounded-t-xl *:[img:last-child]:rounded-b-xl",
    },
    /** Border colour override: accent (new signal row), danger (kill switch on: rgba(240,97,109,0.45)). */
    tone: {
      accent: "border-sg-accent",
      danger: "border-[rgba(240,97,109,0.45)]",
    },
    /** `cardTap`: pressed state for tappable rows (signal card, More item). */
    tap: {
      true: "cursor-pointer [-webkit-tap-highlight-color:transparent] active:bg-sg-raised",
      false: "",
    },
  },
  defaultVariants: {
    variant: "default",
    tap: false,
  },
})

// forwardRef so Card works as a render target / ref holder under React 18.
const Card = React.forwardRef<HTMLDivElement, useRender.ComponentProps<"div"> &
  VariantProps<typeof cardVariants> & { size?: "default" | "sm" }>(function Card({
  className,
  variant = "default",
  tone,
  tap,
  size = "default",
  render,
  ...props
}, ref) {
  return useRender({
    defaultTagName: "div",
    ref,
    props: mergeProps<"div">(
      {
        className: cn(cardVariants({ variant, tone, tap }), className),
      },
      props
    ),
    render,
    state: {
      slot: "card",
      size,
      variant,
    },
  })
})

function CardHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-header"
      className={cn(
        "group/card-header @container/card-header grid auto-rows-min items-start gap-1 rounded-t-xl px-(--card-spacing) has-data-[slot=card-action]:grid-cols-[1fr_auto] has-data-[slot=card-description]:grid-rows-[auto_auto] [.border-b]:pb-(--card-spacing)",
        className
      )}
      {...props}
    />
  )
}

function CardTitle({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-title"
      className={cn(
        "font-heading text-base leading-snug font-medium group-data-[size=sm]/card:text-sm",
        className
      )}
      {...props}
    />
  )
}

function CardDescription({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-description"
      className={cn("text-sm text-muted-foreground", className)}
      {...props}
    />
  )
}

function CardAction({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-action"
      className={cn(
        "col-start-2 row-span-2 row-start-1 self-start justify-self-end",
        className
      )}
      {...props}
    />
  )
}

function CardContent({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-content"
      className={cn("px-(--card-spacing)", className)}
      {...props}
    />
  )
}

function CardFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-footer"
      className={cn(
        "flex items-center rounded-b-xl border-t bg-muted/50 p-(--card-spacing)",
        className
      )}
      {...props}
    />
  )
}

export {
  Card,
  cardVariants,
  CardHeader,
  CardFooter,
  CardTitle,
  CardAction,
  CardDescription,
  CardContent,
}
