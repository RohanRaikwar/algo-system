import { Toggle as TogglePrimitive } from "@base-ui/react/toggle"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"

/*
 * Toggle looks. The app's toggles are segmented-control items (ToggleGroup);
 * pressed state comes from Base UI's data-pressed. `segment` and `compact`
 * are complete looks; `size` only applies to `segment` (tf / md) and to the
 * shadcn variants.
 */
const toggleVariants = cva("", {
  variants: {
    variant: {
      /** shadcn toggle, kept for generic use. */
      default:
        "group/toggle inline-flex items-center justify-center gap-1 rounded-lg text-sm font-medium whitespace-nowrap transition-all outline-none hover:bg-muted hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-50 data-pressed:bg-muted bg-transparent",
      /** shadcn outline toggle, kept for generic use. */
      outline:
        "group/toggle inline-flex items-center justify-center gap-1 rounded-lg text-sm font-medium whitespace-nowrap transition-all outline-none hover:bg-muted hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-50 data-pressed:bg-muted border border-input bg-transparent",
      /** Item of a bg segmented control (TradingChart timeframes, SettingsModal EMA/SMA/SMMA): pressed = hover bg + inset line. */
      segment:
        "border-none rounded-[7px] font-medium cursor-pointer focus-visible:outline-none focus-visible:shadow-ring hover:text-sg-text bg-transparent text-sg-muted data-pressed:bg-sg-hover data-pressed:text-sg-text data-pressed:shadow-[inset_0_0_0_1px_var(--sg-line)] data-pressed:focus-visible:shadow-ring",
      /** Item of the small raised control: DashboardPage chart layout 1 / 2 / 4 (layoutBtn). */
      compact:
        "min-w-[26px] py-[2px] px-[8px] border-0 rounded-[6px] text-[12px] font-semibold cursor-pointer focus-visible:outline-none focus-visible:shadow-ring hover:text-sg-text bg-transparent text-sg-muted data-pressed:bg-sg-hover data-pressed:text-sg-text",
    },
    size: {
      /** shadcn sizes. */
      default: "",
      sm: "",
      lg: "",
      /** TradingChart timeframe button: min 38px, 28px tall; fills the row and grows to 34px on phones. */
      tf: "min-w-[38px] h-[28px] py-0 px-[10px] text-[0.82rem] tabular-nums transition-[background,color] duration-150 ease-[ease] lte-640:flex-1 lte-640:min-w-0 lte-640:px-[4px] lte-640:h-[34px] lte-640:text-[13px] lte-640:font-semibold phone-land:h-[34px]",
      /** SettingsModal indicator type (EMA / SMA / SMMA): 30px, 0.84rem; fills the row on phones. */
      md: "h-[30px] py-0 px-[12px] text-[0.84rem] lte-640:flex-1",
    },
  },
  compoundVariants: [
    { variant: ["default", "outline"], size: "default", class: "h-8 min-w-8 px-2.5" },
    { variant: ["default", "outline"], size: "sm", class: "h-7 min-w-7 rounded-[min(var(--radius-md),12px)] px-2.5 text-[0.8rem]" },
    { variant: ["default", "outline"], size: "lg", class: "h-9 min-w-9 px-2.5" },
  ],
  defaultVariants: {
    variant: "default",
    size: "default",
  },
})

function Toggle({
  className,
  variant = "default",
  size = "default",
  ...props
}: TogglePrimitive.Props & VariantProps<typeof toggleVariants>) {
  return (
    <TogglePrimitive
      data-slot="toggle"
      className={
        typeof className === "function"
          ? (state) => cn(toggleVariants({ variant, size }), className(state))
          : cn(toggleVariants({ variant, size }), className)
      }
      {...props}
    />
  )
}

export { Toggle, toggleVariants }
