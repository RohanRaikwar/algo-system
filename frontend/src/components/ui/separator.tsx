import { Separator as SeparatorPrimitive } from "@base-ui/react/separator"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"

const separatorVariants = cva("shrink-0", {
  variants: {
    variant: {
      /** shadcn separator: 1px line-coloured rule, full width / stretched. */
      default:
        "bg-border data-horizontal:h-px data-horizontal:w-full data-vertical:w-px data-vertical:self-stretch",
      /** SignalsPage tab-group divider (orientation="vertical"): 1x20px, 6px side margin, centred by its row. */
      group: "w-[1px] h-[20px] my-0 mx-[6px] bg-sg-line",
      /** SystemHealth card `divider`: 1px line-soft rule with 8px above and below. */
      soft: "h-[1px] my-[8px] mx-0 bg-sg-line-soft",
    },
  },
  defaultVariants: {
    variant: "default",
  },
})

function Separator({
  className,
  orientation = "horizontal",
  variant = "default",
  ...props
}: SeparatorPrimitive.Props & VariantProps<typeof separatorVariants>) {
  const base = separatorVariants({ variant })
  return (
    <SeparatorPrimitive
      data-slot="separator"
      orientation={orientation}
      className={
        typeof className === "function"
          ? (state) => cn(base, className(state))
          : cn(base, className)
      }
      {...props}
    />
  )
}

export { Separator, separatorVariants }
