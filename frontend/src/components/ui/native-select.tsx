import * as React from "react"
import { cn } from "cn"
import { ChevronDownIcon } from "lucide-react"
import { type VariantProps } from "class-variance-authority"

import { inputVariants } from "@/components/ui/input"

type NativeSelectProps = Omit<React.ComponentProps<"select">, "size"> & {
  size?: "sm" | "default"
  /**
   * App looks (see inputVariants in input.tsx): filter (SignalsPage), sheet
   * (FilterSheet), form (SettingsModal), symbol (TradingChart instrument).
   * These render a bare <select> with the browser arrow (symbol draws its
   * own chevron), no wrapper and no icon, exactly like the old markup;
   * className goes on the select. "default" is shadcn's wrapped select.
   */
  variant?: VariantProps<typeof inputVariants>["variant"]
}

const NativeSelect = React.forwardRef<HTMLSelectElement, NativeSelectProps>(function NativeSelect({
  className,
  size = "default",
  variant = "default",
  ...props
}, ref) {
  if (variant && variant !== "default") {
    return (
      <select
        ref={ref}
        data-slot="native-select"
        className={cn(inputVariants({ variant }), className)}
        {...props}
      />
    )
  }
  return (
    <div
      className={cn(
        "group/native-select relative w-fit has-[select:disabled]:opacity-50",
        className
      )}
      data-slot="native-select-wrapper"
      data-size={size}
    >
      <select
        ref={ref}
        data-slot="native-select"
        data-size={size}
        className="h-8 w-full min-w-0 appearance-none rounded-lg border border-input bg-transparent py-1 pr-8 pl-2.5 text-sm transition-colors outline-none select-none selection:bg-primary selection:text-primary-foreground placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:cursor-not-allowed aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 data-[size=sm]:h-7 data-[size=sm]:rounded-[min(var(--radius-md),10px)] data-[size=sm]:py-0.5 dark:bg-input/30 dark:hover:bg-input/50 dark:aria-invalid:border-destructive/50 dark:aria-invalid:ring-destructive/40"
        {...props}
      />
      <ChevronDownIcon className="pointer-events-none absolute top-1/2 right-2.5 size-4 -translate-y-1/2 text-muted-foreground select-none" aria-hidden="true" data-slot="native-select-icon" />
    </div>
  )
})

/** Plain <option>; the app's selects never styled their options. */
function NativeSelectOption({
  className,
  ...props
}: React.ComponentProps<"option">) {
  return (
    <option
      data-slot="native-select-option"
      className={className}
      {...props}
    />
  )
}

function NativeSelectOptGroup({
  className,
  ...props
}: React.ComponentProps<"optgroup">) {
  return (
    <optgroup
      data-slot="native-select-optgroup"
      className={className}
      {...props}
    />
  )
}

export { NativeSelect, NativeSelectOptGroup, NativeSelectOption }
