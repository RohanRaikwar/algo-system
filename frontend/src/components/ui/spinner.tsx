import { cn } from "cn"
import { RefreshCw, type LucideIcon, type LucideProps } from "lucide-react"

/** The app's spin (signals/classes.ts `spin`): 1s linear, off with reduced motion. */
const spin = "animate-[sg-spin_1s_linear_infinite] motion-reduce:animate-none"

/**
 * Spinning icon. Defaults to RefreshCw, the icon every loading state and
 * refresh button uses; size / strokeWidth pass through as on any lucide
 * icon (no default size class, so size={32} etc. is what you get).
 * spinning={false} renders the still icon (refresh buttons spin only while
 * loading). No role is added: the old icons were decorative.
 */
function Spinner({
  className,
  spinning = true,
  icon: Icon = RefreshCw,
  ...props
}: LucideProps & { spinning?: boolean; icon?: LucideIcon }) {
  return <Icon data-slot="spinner" className={cn(spinning && spin, className)} {...props} />
}

export { Spinner }
