import * as React from "react"
import { Input as InputPrimitive } from "@base-ui/react/input"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"

/*
 * Text field looks, shared by Input and NativeSelect (a select with the same
 * variant gets the same box; selects keep the browser's own arrow, as today).
 * Focus treatment is only on the variants whose original had one; the
 * others rely on their container (Signals page ring) or the browser default,
 * as before. Phone rule: inputs on phone-only sheets use 16px text
 * (variant "sheet") so iOS does not zoom on focus.
 */
const inputVariants = cva("", {
  variants: {
    variant: {
      /** shadcn field, kept for generic use. */
      default:
        "h-8 w-full min-w-0 rounded-lg border border-input bg-transparent px-2.5 py-1 text-base transition-colors outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 md:text-sm",
      /** SignalsPage filter bar (`filterField`): strategy / action selects and token search (add min-w-[200px] on the search). */
      filter:
        "h-[34px] py-0 px-[10px] bg-sg-bg border border-solid border-sg-line rounded-ctrl text-sg-text text-[0.85rem] [transition:border-color_0.15s] hover:border-sg-faint placeholder:text-sg-faint lte-640:flex-[1_1_140px] lte-640:min-w-0",
      /** FilterSheet `control`: 44px, 16px text (no iOS zoom). */
      sheet:
        "h-[44px] py-0 px-[12px] border border-solid border-sg-line rounded-ctrl bg-sg-bg text-sg-text leading-[inherit] text-[16px] font-normal",
      /** SettingsModal `inputCls`: period input and timeframe select, 38px, own focus ring. */
      form:
        "h-[38px] py-0 px-[10px] bg-sg-bg border border-solid border-sg-line rounded-ctrl text-sg-text text-[0.88rem] placeholder:text-sg-faint hover:border-sg-line-hover focus-visible:outline-none focus-visible:shadow-ring",
      /** PositionControls `posInput`: 76px inline number field. */
      xs: "w-[76px] py-[2px] px-[4px] border border-solid border-sg-line rounded-ctrl bg-sg-surface text-sg-text leading-[inherit] text-[0.74rem]",
      /**
       * TradingChart instrument select (`symbolSelect`, select only): raised,
       * drawn chevron, borderless and transparent on phones.
       */
      symbol:
        "h-[34px] py-0 pr-[30px] pl-[10px] bg-sg-raised [background-image:url(data:image/svg+xml,%3Csvg%20xmlns=%27http://www.w3.org/2000/svg%27%20width=%2712%27%20height=%2712%27%20viewBox=%270%200%2024%2024%27%20fill=%27none%27%20stroke=%27%238a94a6%27%20stroke-width=%272.5%27%20stroke-linecap=%27round%27%20stroke-linejoin=%27round%27%3E%3Cpath%20d=%27m6%209%206%206%206-6%27/%3E%3C/svg%3E)] bg-no-repeat [background-position:right_10px_center] border border-solid border-sg-line rounded-ctrl text-sg-text text-[0.9rem] font-semibold appearance-none cursor-pointer max-w-[220px] hover:border-sg-line-hover focus-visible:outline-none focus-visible:shadow-ring lte-640:h-[44px] lte-640:min-w-0 lte-640:max-w-full lte-640:pt-0 lte-640:pr-[22px] lte-640:pb-[14px] lte-640:pl-0 lte-640:border-0 lte-640:bg-transparent lte-640:[background-position:right_4px_top_9px] lte-640:text-[15px]",
    },
  },
  defaultVariants: {
    variant: "default",
  },
})

type InputVariant = Exclude<NonNullable<VariantProps<typeof inputVariants>["variant"]>, "symbol">

// forwardRef: React 18 drops refs on plain function components (SettingsModal
// focuses the Period input through one).
const Input = React.forwardRef<HTMLInputElement, React.ComponentProps<"input"> & { variant?: InputVariant }>(function Input({
  className,
  type,
  variant = "default",
  ...props
}, ref) {
  return (
    <InputPrimitive
      ref={ref}
      type={type}
      data-slot="input"
      className={cn(inputVariants({ variant }), className)}
      {...props}
    />
  )
})

export { Input, inputVariants }
