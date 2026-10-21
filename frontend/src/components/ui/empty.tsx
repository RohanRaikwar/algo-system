import { mergeProps } from "@base-ui/react/merge-props"
import { useRender } from "@base-ui/react/use-render"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"

/*
 * Empty / loading / error state blocks. Put the icon and a <p> straight
 * inside (the variants style descendant svg and p as the old classes did);
 * EmptyHeader / EmptyTitle / … are shadcn's and only fit variant="classic".
 */
const emptyVariants = cva("", {
  variants: {
    variant: {
      /** `emptyState` (signals/classes.ts): centred icon + text, faint icon, p capped at 52ch. */
      default:
        "flex flex-col items-center justify-center gap-[10px] py-[56px] px-[20px] text-center text-sg-muted [&_svg]:text-sg-faint [&_p]:max-w-[52ch] [&_p]:text-[0.9rem]",
      /** `emptyState` with the dashed 12px frame: DailyAnalyticsTab "no trades in range". */
      dashed:
        "flex flex-col items-center justify-center gap-[10px] py-[56px] px-[20px] text-center text-sg-muted [&_svg]:text-sg-faint [&_p]:max-w-[52ch] [&_p]:text-[0.9rem] border border-dashed border-sg-line rounded-[12px]",
      /** SignalsMobile `Empty`: 13px, icon keeps the muted colour. */
      mobile:
        "flex flex-col items-center gap-[10px] py-[48px] px-[24px] text-sg-muted text-center text-[13px]",
      /** Dashed one-line note: SettingsModal "No indicators yet" (render as <p>). */
      note: "p-[14px] border border-dashed border-sg-line rounded-[10px] text-sg-muted text-[0.86rem] text-center",
      /** Dashed one-line note, roomier: SystemHealth `unavailable` (render as <p>). */
      unavailable:
        "py-[18px] px-[14px] border border-dashed border-sg-line rounded-[10px] text-sg-muted text-[0.86rem] leading-[1.5] text-center",
      /** Dashed panel-radius block: SystemHealth "Waiting for the first metrics update". */
      panel: "py-[48px] px-[20px] border border-dashed border-sg-line rounded-panel text-sg-muted text-center",
      /** shadcn empty, kept for generic use. */
      classic:
        "flex w-full min-w-0 flex-1 flex-col items-center justify-center gap-4 rounded-xl border-dashed p-6 text-center text-balance",
    },
  },
  defaultVariants: {
    variant: "default",
  },
})

function Empty({
  className,
  variant = "default",
  render,
  ...props
}: useRender.ComponentProps<"div"> & VariantProps<typeof emptyVariants>) {
  return useRender({
    defaultTagName: "div",
    props: mergeProps<"div">(
      { className: cn(emptyVariants({ variant }), className) },
      props
    ),
    render,
    state: { slot: "empty", variant },
  })
}

function EmptyHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="empty-header"
      className={cn("flex max-w-sm flex-col items-center gap-2", className)}
      {...props}
    />
  )
}

const emptyMediaVariants = cva(
  "mb-2 flex shrink-0 items-center justify-center [&_svg]:pointer-events-none [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        default: "bg-transparent",
        icon: "flex size-8 shrink-0 items-center justify-center rounded-lg bg-muted text-foreground [&_svg:not([class*='size-'])]:size-4",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)

function EmptyMedia({
  className,
  variant = "default",
  ...props
}: React.ComponentProps<"div"> & VariantProps<typeof emptyMediaVariants>) {
  return (
    <div
      data-slot="empty-icon"
      data-variant={variant}
      className={cn(emptyMediaVariants({ variant, className }))}
      {...props}
    />
  )
}

function EmptyTitle({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="empty-title"
      className={cn(
        "font-heading text-sm font-medium tracking-tight",
        className
      )}
      {...props}
    />
  )
}

function EmptyDescription({ className, ...props }: React.ComponentProps<"p">) {
  return (
    <div
      data-slot="empty-description"
      className={cn(
        "text-sm/relaxed text-muted-foreground [&>a]:underline [&>a]:underline-offset-4 [&>a:hover]:text-primary",
        className
      )}
      {...props}
    />
  )
}

function EmptyContent({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="empty-content"
      className={cn(
        "flex w-full max-w-sm min-w-0 flex-col items-center gap-2.5 text-sm text-balance",
        className
      )}
      {...props}
    />
  )
}

export {
  Empty,
  emptyVariants,
  EmptyHeader,
  EmptyTitle,
  EmptyDescription,
  EmptyContent,
  EmptyMedia,
}
