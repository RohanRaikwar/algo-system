import * as React from "react"
import { Button as ButtonPrimitive } from "@base-ui/react/button"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"

/*
 * Button looks of the app. `variant` is the colour family (fill, border,
 * hover, radius); `size` is the geometry (height, padding, font size). The
 * variants marked "self-sized" carry their own geometry and default to
 * size="none". Each variant/size says which element it reproduces.
 *
 * The base holds no height, gap, font size, radius, ring or svg size: every
 * look below states those itself. Focus is the app ring (shadow-ring), the
 * same one the Signals page and the dialogs already draw.
 */
const buttonVariantsBase = cva(
  "inline-flex items-center justify-center cursor-pointer focus-visible:outline-none focus-visible:shadow-ring disabled:cursor-default [&_svg]:pointer-events-none",
  {
    variants: {
      variant: {
        /** Accent fill: SettingsModal Add / Apply, TradingSettingsTab "Allow entries", FilterSheet Done. */
        default: "rounded-ctrl border-0 bg-sg-accent text-white",
        /** Raised + line border, text colour: SettingsModal Cancel, TradingSettingsTab "Keep blocked", PositionControls SL / TGT / Save. */
        secondary:
          "rounded-ctrl border border-solid border-sg-line bg-sg-raised text-sg-text hover:enabled:bg-sg-hover",
        /** Raised + line border, text-2 brightening on hover: `ctrlBtn` (pagination, refresh, retry), TradingChart "Indicators". */
        outline:
          "rounded-ctrl border border-solid border-sg-line bg-sg-raised text-sg-text-2 hover:enabled:bg-sg-hover hover:enabled:text-sg-text",
        /** Raised + loss-soft border, loss text: PositionControls Exit / Confirm exit (posBtnDanger). */
        destructive:
          "rounded-ctrl border border-solid border-sg-loss-soft bg-sg-raised text-sg-loss hover:enabled:bg-sg-hover",
        /** Transparent + line border, no hover: FilterSheet Clear (text-2), PositionControls Cancel / No with tone="muted" (posBtnGhost). */
        bordered:
          "rounded-ctrl border border-solid border-sg-line bg-transparent text-sg-text-2",
        /** Borderless icon button, raised on hover: SettingsModal close (X). */
        ghost:
          "rounded-ctrl border-0 bg-transparent text-sg-muted hover:bg-sg-raised hover:text-sg-text",
        /** Borderless faint icon, loss on hover: SettingsModal indicator trash button. */
        "ghost-danger":
          "rounded-ctrl border-0 bg-transparent text-sg-faint hover:bg-sg-loss-soft hover:text-sg-loss",
        /** Borderless tool icon (text-2, hover/active bg-hover): TradingChart compact "Indicators" icon button (badge inside). */
        "ghost-tool":
          "relative shrink-0 gap-[6px] min-w-[40px] p-0 rounded-ctrl border-0 bg-transparent text-sg-text-2 text-[0.82rem] font-medium whitespace-nowrap hover:bg-sg-hover hover:text-sg-text active:bg-sg-hover",
        /** Round touch icon, bg-hover while pressed: BottomSheet close (muted), SignalsMobile back (tone="accent"). */
        "ghost-touch":
          "rounded-pill border-0 bg-transparent text-sg-muted active:bg-sg-hover",
        /** Borderless icon with no hover: SRSheet close. */
        "ghost-quiet": "shrink-0 rounded-ctrl border-0 bg-transparent text-sg-muted",
        /** Bordered square on surface; data-active="true" turns it accent: SignalsMobile filter button. */
        "segment-icon":
          "relative shrink-0 rounded-[10px] border border-solid border-sg-line bg-sg-surface text-sg-text-2 active:bg-sg-hover data-[active=true]:border-sg-accent data-[active=true]:text-sg-accent",
        /** Self-sized. Raised header pill, font inherited: PushToggle bell / Test (tone="profit" when on, text-2 otherwise). */
        pill: "gap-[5px] h-[28px] px-[9px] rounded-pill border-none bg-sg-raised text-sg-text-2 leading-[inherit] whitespace-nowrap min-w-0 phone-land:h-[26px] hover:enabled:bg-sg-hover disabled:opacity-60 disabled:cursor-progress",
        /** Self-sized. Outline pill turning accent on hover: SettingsModal quick-add presets. */
        chip: "h-[28px] py-0 px-[11px] rounded-pill border border-solid border-sg-line bg-transparent text-sg-text-2 text-[0.8rem] tabular-nums hover:border-sg-accent hover:text-sg-text hover:bg-sg-accent-soft portrait-640:min-h-[36px] portrait-640:px-[14px]",
        /** Self-sized. Accent-soft removable pill: SignalsMobile active-filter chips. */
        "filter-chip":
          "gap-[4px] h-[30px] py-0 pr-[10px] pl-[12px] rounded-pill border-0 bg-sg-accent-soft text-sg-accent text-[12px] font-semibold",
        /** Self-sized. Accent text, accent-soft on hover: SignalsPage "Clear filters". */
        link: "h-[34px] py-0 px-[10px] rounded-ctrl border-0 bg-transparent text-sg-accent text-[0.85rem] hover:bg-sg-accent-soft",
        /** Self-sized. Muted text turning loss on hover: SettingsModal "Remove all". */
        "danger-text":
          "h-[34px] py-0 px-[8px] rounded-ctrl border-0 bg-transparent text-sg-muted text-[0.84rem] hover:enabled:text-sg-loss disabled:opacity-[0.4]",
      },
      size: {
        /** 32px, 0.82rem, normal weight, .45 disabled: `ctrlBtn`. */
        default:
          "h-[32px] py-0 px-[12px] gap-[6px] text-[0.82rem] [transition:background_0.15s,color_0.15s] disabled:opacity-[0.45]",
        /** 32px, 0.84rem, medium, .5 disabled: TradingSettingsTab buttons (Keep blocked / Allow entries). */
        md: "h-[32px] py-0 px-[12px] text-[0.84rem] font-medium disabled:opacity-[0.5]",
        /** 38px, 0.88rem, medium (semibold with variant default): SettingsModal Add / Apply / Cancel. */
        lg: "h-[38px] py-0 px-[14px] gap-[6px] text-[0.88rem] font-medium",
        /** 30px, 40px tall on phones: TradingChart "Indicators" button. */
        sm: "h-[30px] py-0 px-[11px] gap-[6px] text-[0.82rem] font-medium whitespace-nowrap shrink-0 lte-640:h-[40px] lte-640:min-w-[44px] lte-640:px-[10px] phone-land:h-[40px] phone-land:min-w-[44px]",
        /** Tiny row action: PositionControls buttons (posBtnBase). */
        xs: "py-[2px] px-[8px] text-[0.72rem] font-semibold",
        /** 44px full-width sheet action: FilterSheet Clear / Done. */
        sheet:
          "flex-1 h-[44px] leading-[inherit] font-semibold active:brightness-[1.15] disabled:opacity-[0.45]",
        /** Square icon buttons, by pixel size. */
        "icon-30": "w-[30px] h-[30px]",
        "icon-32": "w-[32px] h-[32px]",
        "icon-40": "w-[40px] h-[40px]",
        "icon-42": "w-[42px] h-[42px]",
        "icon-44": "w-[44px] h-[44px]",
        /** No geometry: the self-sized variants (pill, chip, filter-chip, link, danger-text). */
        none: "",
        /* shadcn icon sizes, kept for generic use. */
        icon: "size-8",
        "icon-xs": "size-6",
        "icon-sm": "size-7",
        "icon-lg": "size-9",
      },
      /** Text colour override, e.g. tone="muted" on `bordered`, tone="accent" on `ghost-touch`, tone="profit" on `pill`. */
      tone: {
        muted: "text-sg-muted",
        faint: "text-sg-faint",
        text: "text-sg-text",
        "text-2": "text-sg-text-2",
        accent: "text-sg-accent",
        profit: "text-sg-profit",
        loss: "text-sg-loss",
        warn: "text-sg-warn",
      },
    },
    compoundVariants: [
      /* Accent fill hover everywhere except the touch sheet. */
      {
        variant: "default",
        size: ["default", "md", "lg", "sm", "xs"],
        class: "hover:enabled:bg-sg-accent-hover",
      },
      /* SettingsModal primary: semibold, disabled turns raised + faint. */
      {
        variant: "default",
        size: "lg",
        class: "font-semibold disabled:bg-sg-raised disabled:text-sg-faint",
      },
    ],
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  }
)

type ButtonVariantProps = VariantProps<typeof buttonVariantsBase>

/** Variants that carry their own geometry; their size defaults to "none". */
const SELF_SIZED = new Set(["pill", "chip", "filter-chip", "link", "danger-text"])

function buttonVariants({
  variant,
  size,
  tone,
  className,
}: ButtonVariantProps & { className?: string } = {}) {
  const resolved = size ?? (variant && SELF_SIZED.has(variant) ? "none" : "default")
  return cn(buttonVariantsBase({ variant, size: resolved, tone }), className)
}

// forwardRef: React 18 drops refs on plain function components, and Base UI
// triggers (render={<Button/>}) need the ref to anchor and focus.
const Button = React.forwardRef<HTMLElement, ButtonPrimitive.Props & ButtonVariantProps>(function Button({
  className,
  variant = "default",
  size,
  tone,
  ...props
}, ref) {
  return (
    <ButtonPrimitive
      ref={ref}
      data-slot="button"
      className={
        typeof className === "function"
          ? (state) => buttonVariants({ variant, size, tone, className: className(state) })
          : buttonVariants({ variant, size, tone, className })
      }
      {...props}
    />
  )
})

export { Button, buttonVariants }
