"use client"

import { Switch as SwitchPrimitive } from "@base-ui/react/switch"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"

/*
 * Switch looks: a 44x26 track with a 20px thumb that slides 18px. The old
 * switches drew the thumb with ::after; here it is the Thumb element with
 * the same box. State comes from Base UI's data-checked / data-disabled.
 */
const switchVariants = cva("", {
  variants: {
    variant: {
      /** SettingsModal overlay toggle as drawn at portrait-640 (line track, accent when on, white thumb). */
      default:
        "relative shrink-0 w-[44px] h-[26px] rounded-[13px] bg-sg-line motion-safe:[transition:background_0.15s] cursor-pointer data-checked:bg-sg-accent focus-visible:outline-none focus-visible:shadow-ring",
      /** TradingSettingsTab kill switch: inset-line track, loss red when on (no ring when on), .5 disabled. */
      danger:
        "relative shrink-0 w-[44px] h-[26px] p-0 border-0 rounded-pill bg-sg-hover shadow-[inset_0_0_0_1px_var(--sg-line)] cursor-pointer [transition:background_0.18s] data-checked:bg-sg-loss data-checked:[box-shadow:none] focus-visible:outline-none focus-visible:shadow-ring data-checked:focus-visible:[box-shadow:none] data-disabled:opacity-[0.5] data-disabled:cursor-default",
      /** shadcn switch, kept for generic use (size sm / default). */
      classic:
        "peer group/switch relative inline-flex shrink-0 items-center rounded-full border border-transparent transition-all outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 data-[size=default]:h-[18.4px] data-[size=default]:w-[32px] data-[size=sm]:h-[14px] data-[size=sm]:w-[24px] data-checked:bg-primary data-unchecked:bg-input/80 data-disabled:cursor-not-allowed data-disabled:opacity-50",
    },
  },
  defaultVariants: {
    variant: "default",
  },
})

const thumbVariants = cva("pointer-events-none block", {
  variants: {
    variant: {
      default:
        "absolute top-[3px] left-[3px] w-[20px] h-[20px] rounded-[50%] bg-white motion-safe:[transition:transform_0.15s] data-checked:[transform:translateX(18px)]",
      danger:
        "absolute top-[3px] left-[3px] w-[20px] h-[20px] rounded-[50%] bg-sg-text-2 [transition:transform_0.18s,background_0.18s] data-checked:[transform:translateX(18px)] data-checked:bg-white motion-reduce:[transition:none]!",
      classic:
        "rounded-full ring-0 transition-transform group-data-[size=default]/switch:size-4 group-data-[size=sm]/switch:size-3 group-data-[size=default]/switch:data-checked:translate-x-[calc(100%-2px)] group-data-[size=sm]/switch:data-checked:translate-x-[calc(100%-2px)] data-checked:bg-primary-foreground data-unchecked:translate-x-0 data-unchecked:bg-foreground",
    },
  },
  defaultVariants: {
    variant: "default",
  },
})

function Switch({
  className,
  variant = "default",
  size = "default",
  ...props
}: SwitchPrimitive.Root.Props &
  VariantProps<typeof switchVariants> & {
    size?: "sm" | "default"
  }) {
  const rootClass = switchVariants({ variant })
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      data-size={size}
      className={
        typeof className === "function"
          ? (state) => cn(rootClass, className(state))
          : cn(rootClass, className)
      }
      {...props}
    >
      <SwitchPrimitive.Thumb
        data-slot="switch-thumb"
        className={thumbVariants({ variant })}
      />
    </SwitchPrimitive.Root>
  )
}

export { Switch, switchVariants }
