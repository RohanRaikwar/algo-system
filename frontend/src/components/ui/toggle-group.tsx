"use client"

import * as React from "react"
import { Toggle as TogglePrimitive } from "@base-ui/react/toggle"
import { ToggleGroup as ToggleGroupPrimitive } from "@base-ui/react/toggle-group"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"

import { toggleVariants } from "@/components/ui/toggle"

/*
 * Segmented controls. The group takes variant (and size for `segment`) and
 * hands both to its items. Value is an array: value={[tf]} and
 * onValueChange={(v) => v[0] && set(v[0])}.
 *
 *   variant="segment" size="tf"  TradingChart timeframes
 *   variant="segment" size="md"  SettingsModal EMA / SMA / SMMA (add lte-640:w-full on the group)
 *   variant="compact"            DashboardPage chart layout 1 / 2 / 4
 */
const toggleGroupVariants = cva("group/toggle-group", {
  variants: {
    variant: {
      /** shadcn group, kept for generic use (spacing via --gap). */
      default:
        "flex w-fit flex-row items-center gap-[--spacing(var(--gap))] rounded-lg data-[size=sm]:rounded-[min(var(--radius-md),10px)] data-vertical:flex-col data-vertical:items-stretch",
      outline:
        "flex w-fit flex-row items-center gap-[--spacing(var(--gap))] rounded-lg data-[size=sm]:rounded-[min(var(--radius-md),10px)] data-vertical:flex-col data-vertical:items-stretch",
      /** bg segmented frame, 10px radius (timeframes, indicator type). */
      segment: "inline-flex p-[3px] gap-[2px] bg-sg-bg border border-solid border-sg-line rounded-[10px]",
      /** small raised frame (layoutGroup). */
      compact: "inline-flex p-[2px] gap-[2px] rounded-ctrl bg-sg-raised",
    },
  },
  defaultVariants: {
    variant: "default",
  },
})

/* shadcn item extras (joined borders when spacing=0), shadcn variants only. */
const shadcnItem =
  "shrink-0 group-data-[spacing=0]/toggle-group:rounded-none group-data-[spacing=0]/toggle-group:px-2 focus:z-10 focus-visible:z-10 group-data-horizontal/toggle-group:data-[spacing=0]:first:rounded-l-lg group-data-vertical/toggle-group:data-[spacing=0]:first:rounded-t-lg group-data-horizontal/toggle-group:data-[spacing=0]:last:rounded-r-lg group-data-vertical/toggle-group:data-[spacing=0]:last:rounded-b-lg group-data-horizontal/toggle-group:data-[spacing=0]:data-[variant=outline]:border-l-0 group-data-vertical/toggle-group:data-[spacing=0]:data-[variant=outline]:border-t-0 group-data-horizontal/toggle-group:data-[spacing=0]:data-[variant=outline]:first:border-l group-data-vertical/toggle-group:data-[spacing=0]:data-[variant=outline]:first:border-t"

type ToggleVariantProps = VariantProps<typeof toggleVariants>

const ToggleGroupContext = React.createContext<
  ToggleVariantProps & {
    spacing?: number
    orientation?: "horizontal" | "vertical"
  }
>({
  size: "default",
  variant: "default",
  spacing: 2,
  orientation: "horizontal",
})

function ToggleGroup({
  className,
  variant = "default",
  size,
  spacing = 2,
  orientation = "horizontal",
  children,
  ...props
}: ToggleGroupPrimitive.Props &
  ToggleVariantProps & {
    spacing?: number
    orientation?: "horizontal" | "vertical"
  }) {
  const shadcn = variant === "default" || variant === "outline"
  const groupClass = toggleGroupVariants({ variant })
  return (
    <ToggleGroupPrimitive
      data-slot="toggle-group"
      data-variant={variant}
      data-size={size}
      data-spacing={spacing}
      data-orientation={orientation}
      style={shadcn ? ({ "--gap": spacing } as React.CSSProperties) : undefined}
      className={
        typeof className === "function"
          ? (state) => cn(groupClass, className(state))
          : cn(groupClass, className)
      }
      {...props}
    >
      <ToggleGroupContext.Provider
        value={{ variant, size, spacing, orientation }}
      >
        {children}
      </ToggleGroupContext.Provider>
    </ToggleGroupPrimitive>
  )
}

function ToggleGroupItem({
  className,
  children,
  variant = "default",
  size = "default",
  ...props
}: TogglePrimitive.Props & ToggleVariantProps) {
  const context = React.useContext(ToggleGroupContext)
  const v = context.variant || variant
  const s = context.size || size
  const shadcn = v === "default" || v === "outline"
  const itemClass = cn(shadcn && shadcnItem, toggleVariants({ variant: v, size: s }))

  return (
    <TogglePrimitive
      data-slot="toggle-group-item"
      data-variant={v}
      data-size={s}
      data-spacing={context.spacing}
      className={
        typeof className === "function"
          ? (state) => cn(itemClass, className(state))
          : cn(itemClass, className)
      }
      {...props}
    >
      {children}
    </TogglePrimitive>
  )
}

export { ToggleGroup, ToggleGroupItem, toggleGroupVariants }
