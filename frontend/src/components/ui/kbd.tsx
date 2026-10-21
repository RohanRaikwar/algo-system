import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"

/* kbd defaults to the mono font in preflight; both app keys use the page font. */
const kbdVariants = cva("", {
  variants: {
    variant: {
      /** SettingsModal footer keys (Ctrl / Enter / Esc): raised, 2px bottom border. */
      default:
        "inline-block min-w-[20px] py-0 px-[5px] border border-solid border-sg-line border-b-[2px] rounded-[5px] bg-sg-raised text-sg-text-2 [font-family:inherit] text-[0.72rem] leading-[1.5] text-center",
      /** TradingChart "Indicators" button "/" hint: bg, muted (hide it with phone-land:hidden touch:hidden). */
      tool: "min-w-[18px] py-0 px-[4px] border border-solid border-sg-line border-b-2 rounded-[5px] bg-sg-bg text-sg-muted [font-family:inherit] text-[0.72rem] leading-[1.4] text-center",
      /** shadcn kbd, kept for generic use. */
      classic:
        "pointer-events-none inline-flex h-5 w-fit min-w-5 items-center justify-center gap-1 rounded-sm bg-muted px-1 font-sans text-xs font-medium text-muted-foreground select-none",
    },
  },
  defaultVariants: {
    variant: "default",
  },
})

function Kbd({
  className,
  variant = "default",
  ...props
}: React.ComponentProps<"kbd"> & VariantProps<typeof kbdVariants>) {
  return (
    <kbd
      data-slot="kbd"
      className={cn(kbdVariants({ variant }), className)}
      {...props}
    />
  )
}

function KbdGroup({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <kbd
      data-slot="kbd-group"
      className={cn("inline-flex items-center gap-1", className)}
      {...props}
    />
  )
}

export { Kbd, KbdGroup, kbdVariants }
