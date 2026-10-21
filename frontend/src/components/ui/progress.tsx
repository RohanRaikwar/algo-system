import { Progress as ProgressPrimitive } from "@base-ui/react/progress"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"

/*
 * variant="meter" is the SystemHealth usage bar: a 6px raised pill track
 * with a tone-coloured fill that animates its width. The root has no box
 * of its own (put the old margins mt-[2px] mb-[8px] on it). Base UI sets the
 * fill width from value (0-100) and gives it height: inherit.
 * variant="default" is shadcn's (label/value children + 4px track).
 */
const progressVariants = cva("", {
  variants: {
    variant: {
      default: "flex flex-wrap gap-3",
      meter: "",
    },
  },
  defaultVariants: { variant: "default" },
})

const trackVariants = cva("", {
  variants: {
    variant: {
      default: "relative flex h-1 w-full items-center overflow-x-hidden rounded-full bg-muted",
      meter: "h-[6px] rounded-pill bg-sg-raised overflow-hidden",
    },
  },
  defaultVariants: { variant: "default" },
})

const indicatorVariants = cva("", {
  variants: {
    variant: {
      default: "h-full bg-primary transition-all",
      meter: "block h-full rounded-[inherit] transition-[width] duration-400 ease-[ease] motion-reduce:transition-none",
    },
    /** Fill colour (SystemHealth FILL_BG): ok = accent, warn, bad = loss, idle = faint. */
    tone: {
      accent: "bg-sg-accent",
      warn: "bg-sg-warn",
      loss: "bg-sg-loss",
      faint: "bg-sg-faint",
    },
  },
  defaultVariants: { variant: "default" },
})

type ProgressVariantProps = VariantProps<typeof progressVariants> &
  Pick<VariantProps<typeof indicatorVariants>, "tone">

function Progress({
  className,
  children,
  value,
  variant = "default",
  tone,
  ...props
}: ProgressPrimitive.Root.Props & ProgressVariantProps) {
  const base = progressVariants({ variant })
  return (
    <ProgressPrimitive.Root
      value={value}
      data-slot="progress"
      className={
        typeof className === "function"
          ? (state) => cn(base, className(state))
          : cn(base, className)
      }
      {...props}
    >
      {children}
      <ProgressTrack className={trackVariants({ variant })}>
        <ProgressIndicator className={indicatorVariants({ variant, tone })} />
      </ProgressTrack>
    </ProgressPrimitive.Root>
  )
}

function ProgressTrack({ className, ...props }: ProgressPrimitive.Track.Props) {
  return (
    <ProgressPrimitive.Track
      className={className}
      data-slot="progress-track"
      {...props}
    />
  )
}

function ProgressIndicator({
  className,
  ...props
}: ProgressPrimitive.Indicator.Props) {
  return (
    <ProgressPrimitive.Indicator
      data-slot="progress-indicator"
      className={className}
      {...props}
    />
  )
}

function ProgressLabel({ className, ...props }: ProgressPrimitive.Label.Props) {
  return (
    <ProgressPrimitive.Label
      className={cn("text-sm font-medium", className as string)}
      data-slot="progress-label"
      {...props}
    />
  )
}

function ProgressValue({ className, ...props }: ProgressPrimitive.Value.Props) {
  return (
    <ProgressPrimitive.Value
      className={cn(
        "ml-auto text-sm text-muted-foreground tabular-nums",
        className as string
      )}
      data-slot="progress-value"
      {...props}
    />
  )
}

export {
  Progress,
  ProgressTrack,
  ProgressIndicator,
  ProgressLabel,
  ProgressValue,
}
