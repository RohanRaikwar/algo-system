import * as React from "react"
import { mergeProps } from "@base-ui/react/merge-props"
import { useRender } from "@base-ui/react/use-render"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"

/*
 * Badge looks of the app. `variant` is the shape (padding, font size,
 * display) of one existing pill family; `tone` is its colour pair (text +
 * background). Every variant has a default colour, so tone is optional.
 * No base classes: display, gap and line-height differ per family and are
 * stated by each variant (an inline tag and an inline-flex pill do not have
 * the same height).
 */
const pill =
  "inline-flex items-center justify-center gap-[5px] py-[3px] px-[9px] rounded-pill text-[0.74rem] font-semibold leading-[1.3] whitespace-nowrap"

/* shadcn's own badge geometry, used only by its legacy variants below. */
const shadcn =
  "inline-flex h-5 w-fit shrink-0 items-center justify-center gap-1 overflow-hidden rounded-4xl border border-transparent px-2 py-0.5 text-xs font-medium whitespace-nowrap"

const badgeVariants = cva("", {
  variants: {
    variant: {
      /** `actionBadge` / `statusCompleteBadge` pill (signals/classes.ts `pill`). Default tone muted. */
      default: `${pill} text-sg-muted bg-sg-raised`,
      /** `marketBadge`: pill with min-w 74px (TRENDING / SIDEWAYS / …). */
      market: `${pill} min-w-[74px] text-sg-muted bg-sg-raised`,
      /** `orderTypeBadge`: pill with min-w 62px and a leading 6px dot (Paper / Real / …). */
      order: `${pill} min-w-[62px] before:content-[''] before:w-[6px] before:h-[6px] before:rounded-[50%] before:bg-current text-sg-muted bg-sg-raised`,
      /** `ewBadge`: small inline tag (hold / tighten / exit). */
      tag: "py-[1px] px-[7px] rounded-pill text-[0.68rem] font-semibold",
      /** `slKind`: small inline lowercase tag (trail / hard / manual, SHORT, real). */
      kind: "py-[1px] px-[7px] rounded-pill text-[0.68rem] font-semibold lowercase",
      /** `fnoBadge`: F&O card header pill (place with ml-auto). Default tone profit. */
      fno: "py-[2px] px-[9px] rounded-pill text-[0.74rem] font-semibold text-sg-profit bg-sg-profit-soft",
      /** sr/srClasses.ts `mobilePill`: peek strip / sheet regime + Paper pill. Default tone neutral. */
      mobile: "shrink-0 py-[2px] px-[8px] rounded-pill text-[11px] font-semibold tracking-[0.02em] text-sg-muted bg-sg-line-soft",
      /** SRSheet CALL / PUT side pill (tone profit / loss). */
      side: "py-[1px] px-[8px] rounded-pill text-[11px] font-bold",
      /** SRSheet settings chips (Trades, Day, Fade…). Default tone neutral, "on" = tone profit. */
      chip: "py-[4px] px-[10px] rounded-pill text-[12px] tabular-nums text-sg-muted bg-sg-line-soft",
      /** SystemHealth "Market open/closed" card pill. Default tone muted; open = tone profit. */
      status: "py-[2px] px-[9px] rounded-pill text-[0.76rem] font-medium text-sg-muted bg-sg-raised",
      /** SettingsModal "On chart" count next to the section title. */
      count: "min-w-[20px] py-0 px-[6px] rounded-pill text-[0.74rem] font-medium text-center tabular-nums text-sg-muted bg-sg-raised",
      /** SettingsModal indicator row timeframe pill. */
      meta: "py-[1px] px-[8px] rounded-pill text-[0.76rem] tabular-nums text-sg-muted bg-sg-raised",
      /** Accent counter, 18px, nav: SignalBadge unread count (place with ml-[4px]). */
      "count-nav": "inline-flex items-center justify-center min-w-[18px] h-[18px] py-0 px-[5px] rounded-pill bg-sg-accent text-white text-[0.68rem] font-semibold leading-none tabular-nums",
      /** Accent counter inside a tab: SignalsPage LIVE tab open-orders count. */
      "count-tab": "min-w-[18px] py-[1px] px-[6px] rounded-pill bg-sg-accent text-white text-[0.7rem] font-semibold text-center tabular-nums",
      /** Accent counter, 17px: SignalsMobile Positions segment count. */
      "count-xs": "min-w-[17px] py-0 px-[5px] rounded-pill bg-sg-accent text-white text-[10px] leading-[17px]",
      /** Accent counter: TradingChart "Indicators" button count (toolCount, 18px). */
      "count-tool": "min-w-[18px] py-0 px-[5px] rounded-pill bg-sg-accent text-white text-[0.7rem] leading-[18px] font-semibold text-center tabular-nums",
      /** Accent counter: TradingChart compact icon count (toolCount, 16px; position it with absolute top-[2px] right-0). */
      "count-tool-sm": "min-w-[16px] py-0 px-[5px] rounded-pill bg-sg-accent text-white text-[10px] leading-[16px] font-semibold text-center tabular-nums",
      /**
       * Header status pill (market / feed). Display is the caller's
       * (`inline-flex lte-640:hidden phone-land:hidden` or
       * `hidden lte-640:inline-flex phone-land:inline-flex`). Default colour
       * raised / text-2; the compact pill passes its state bg as tone.
       */
      header: "items-center gap-[7px] h-[28px] px-[11px] rounded-pill text-[0.8rem] font-medium whitespace-nowrap min-w-0 lte-640:h-[28px] lte-640:px-[10px] lte-640:text-[12px] phone-land:h-[26px] phone-land:text-[12px] bg-sg-raised text-sg-text-2",

      /* shadcn legacy variants, on the app tokens. */
      secondary: `${shadcn} bg-secondary text-secondary-foreground`,
      destructive: `${shadcn} bg-sg-loss-soft text-sg-loss`,
      outline: `${shadcn} border-border text-foreground`,
      ghost: `${shadcn} hover:bg-muted hover:text-muted-foreground`,
      link: `${shadcn} text-primary underline-offset-4 hover:underline`,
    },
    /**
     * Colour pair. muted = raised/muted, neutral = line-soft/muted (REGIME
     * DEAD/WARMING, SR chips off), exit = raised/text-2 (EXIT action),
     * solid-loss = white on loss (Real order). For the header variant use
     * the bg-only / text-only tones (bg-*, text-*) so its two maps stay
     * independent.
     */
    tone: {
      muted: "text-sg-muted bg-sg-raised",
      neutral: "text-sg-muted bg-sg-line-soft",
      exit: "text-sg-text-2 bg-sg-raised",
      profit: "text-sg-profit bg-sg-profit-soft",
      loss: "text-sg-loss bg-sg-loss-soft",
      warn: "text-sg-warn bg-sg-warn-soft",
      info: "text-sg-info bg-sg-info-soft",
      accent: "text-sg-accent bg-sg-accent-soft",
      "solid-loss": "text-white bg-sg-loss",
      "solid-accent": "text-white bg-sg-accent",
      "bg-raised": "bg-sg-raised",
      "bg-profit": "bg-sg-profit-soft",
      "bg-warn": "bg-sg-warn-soft",
    },
  },
  defaultVariants: {
    variant: "default",
  },
})

type BadgeVariantProps = VariantProps<typeof badgeVariants>

// forwardRef so Badge works as a Base UI trigger under React 18.
const Badge = React.forwardRef<HTMLSpanElement, useRender.ComponentProps<"span"> & BadgeVariantProps>(function Badge({
  className,
  variant = "default",
  tone,
  render,
  ...props
}, ref) {
  return useRender({
    defaultTagName: "span",
    ref,
    props: mergeProps<"span">(
      {
        className: cn(badgeVariants({ variant, tone }), className),
      },
      props
    ),
    render,
    state: {
      slot: "badge",
      variant,
    },
  })
})

export { Badge, badgeVariants }
export type BadgeTone = NonNullable<BadgeVariantProps["tone"]>
