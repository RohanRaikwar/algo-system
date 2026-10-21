import { Checkbox as CheckboxPrimitive } from "@base-ui/react/checkbox"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"
import { CheckIcon } from "lucide-react"

/*
 * The only checkbox today (SettingsModal overlay row on desktop) is the
 * browser's native <input type="checkbox"> with no styling, which a Base UI
 * checkbox cannot reproduce pixel for pixel. `default` is a 13px box on the
 * app tokens close to it (same size and position); keep the native input
 * where an exact match matters.
 */
const checkboxVariants = cva(
  "peer relative flex shrink-0 items-center justify-center cursor-pointer outline-none",
  {
    variants: {
      variant: {
        /** SettingsModal overlay checkbox (desktop): 13px, muted border, accent when checked. */
        default:
          "size-[13px] rounded-[3px] border border-solid border-sg-muted bg-transparent text-white data-checked:bg-sg-accent data-checked:border-sg-accent focus-visible:shadow-ring data-disabled:opacity-[0.5] data-disabled:cursor-default",
        /** shadcn checkbox, kept for generic use. */
        classic:
          "size-4 rounded-[4px] border border-input transition-colors focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 data-disabled:cursor-not-allowed data-disabled:opacity-50 bg-input/30 data-checked:border-primary data-checked:bg-primary data-checked:text-primary-foreground",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)

function Checkbox({
  className,
  variant = "default",
  ...props
}: CheckboxPrimitive.Root.Props & VariantProps<typeof checkboxVariants>) {
  const rootClass = checkboxVariants({ variant })
  return (
    <CheckboxPrimitive.Root
      data-slot="checkbox"
      className={
        typeof className === "function"
          ? (state) => cn(rootClass, className(state))
          : cn(rootClass, className)
      }
      {...props}
    >
      <CheckboxPrimitive.Indicator
        data-slot="checkbox-indicator"
        className={cn(
          "grid place-content-center text-current transition-none",
          variant === "classic" ? "[&>svg]:size-3.5" : "[&>svg]:size-[11px]"
        )}
      >
        <CheckIcon strokeWidth={variant === "classic" ? 2 : 3} />
      </CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  )
}

export { Checkbox, checkboxVariants }
