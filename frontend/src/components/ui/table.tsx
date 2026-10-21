import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"

/*
 * Tables. variant="data" (the default) is the Signals table
 * (signals/classes.ts `table`, `th`, `thNum`, `cell()`): a bare <table> with
 * no scroll container of its own (it sits in the `tableWrap` box, whose
 * scroll keeps the sticky header working). Rows are styled from the table
 * (tbody row borders and hover), so TableRow / TableHeader / TableBody add
 * nothing in this variant. variant="classic" is shadcn's table, wrapped in
 * its overflow container, and its row / head / cell classes.
 */
type TableVariant = "data" | "classic"

const TableContext = React.createContext<TableVariant>("data")

function Table({
  className,
  variant = "data",
  ...props
}: React.ComponentProps<"table"> & { variant?: TableVariant }) {
  if (variant === "data") {
    return (
      <TableContext.Provider value="data">
        <table
          data-slot="table"
          className={cn(
            "w-full min-w-[880px] border-collapse table-fixed [&_tbody_tr]:border-b [&_tbody_tr]:border-b-sg-line-soft [&_tbody_tr]:[transition:background_0.12s] [&_tbody_tr:last-child]:[border-bottom:none] [&_tbody_tr:hover]:bg-sg-surface",
            className
          )}
          {...props}
        />
      </TableContext.Provider>
    )
  }
  return (
    <TableContext.Provider value="classic">
      <div data-slot="table-container" className="relative w-full overflow-x-auto">
        <table
          data-slot="table"
          className={cn("w-full caption-bottom text-sm", className)}
          {...props}
        />
      </div>
    </TableContext.Provider>
  )
}

function useClassic() {
  return React.useContext(TableContext) === "classic"
}

function TableHeader({ className, ...props }: React.ComponentProps<"thead">) {
  const classic = useClassic()
  return (
    <thead
      data-slot="table-header"
      className={cn(classic && "[&_tr]:border-b", className)}
      {...props}
    />
  )
}

function TableBody({ className, ...props }: React.ComponentProps<"tbody">) {
  const classic = useClassic()
  return (
    <tbody
      data-slot="table-body"
      className={cn(classic && "[&_tr:last-child]:border-0", className)}
      {...props}
    />
  )
}

function TableFooter({ className, ...props }: React.ComponentProps<"tfoot">) {
  const classic = useClassic()
  return (
    <tfoot
      data-slot="table-footer"
      className={cn(
        classic && "border-t bg-muted/50 font-medium [&>tr]:last:border-b-0",
        className
      )}
      {...props}
    />
  )
}

function TableRow({ className, ...props }: React.ComponentProps<"tr">) {
  const classic = useClassic()
  return (
    <tr
      data-slot="table-row"
      className={cn(
        classic &&
          "border-b transition-colors hover:bg-muted/50 has-aria-expanded:bg-muted/50 data-[state=selected]:bg-muted",
        className
      )}
      {...props}
    />
  )
}

/** Signals header cell (`th` / `thNum`): sticky raised header, 0.78rem muted. numeric = right aligned. */
const tableHeadVariants = cva(
  "sticky top-0 z-[1] py-[10px] px-[12px] bg-sg-raised border-b border-b-sg-line text-sg-muted text-[0.78rem] font-medium whitespace-nowrap",
  {
    variants: {
      numeric: {
        false: "text-left",
        true: "text-right",
      },
    },
    defaultVariants: { numeric: false },
  }
)

function TableHead({
  className,
  numeric,
  ...props
}: React.ComponentProps<"th"> & VariantProps<typeof tableHeadVariants>) {
  const classic = useClassic()
  return (
    <th
      data-slot="table-head"
      className={cn(
        classic
          ? "h-10 px-2 text-left align-middle font-medium whitespace-nowrap text-foreground [&:has([role=checkbox])]:pr-0"
          : tableHeadVariants({ numeric }),
        className
      )}
      {...props}
    />
  )
}

/**
 * Signals body cell (`cell(compact, kind, tone)`). density: compact = 9px
 * vertical padding (compact tables), default = 10px (the log). kind:
 * plain / strategy (semibold, text colour) / time / price (right, medium) /
 * reason. tone replaces the text-2 colour with a P&L colour (pnlTone).
 */
const tableCellVariants = cva(
  "px-[12px] text-[0.86rem] align-middle overflow-hidden text-ellipsis",
  {
    variants: {
      density: {
        default: "py-[10px]",
        compact: "py-[9px]",
      },
      kind: {
        plain: "text-sg-text-2",
        strategy: "font-semibold whitespace-nowrap text-sg-text",
        time: "tabular-nums whitespace-nowrap text-sg-text-2",
        price: "text-right tabular-nums font-medium whitespace-nowrap text-sg-text-2",
        reason: "whitespace-nowrap text-sg-text-2",
      },
      tone: {
        profit: "text-sg-profit",
        loss: "text-sg-loss",
        muted: "text-sg-muted",
        warn: "text-sg-warn",
      },
    },
    compoundVariants: [
      /* The strategy cell keeps the text colour whatever the tone (as cell() did). */
      { kind: "strategy", tone: ["profit", "loss", "muted", "warn"], class: "text-sg-text" },
    ],
    defaultVariants: { density: "default", kind: "plain" },
  }
)

function TableCell({
  className,
  density,
  kind,
  tone,
  ...props
}: React.ComponentProps<"td"> & VariantProps<typeof tableCellVariants>) {
  const classic = useClassic()
  return (
    <td
      data-slot="table-cell"
      className={cn(
        classic
          ? "p-2 align-middle whitespace-nowrap [&:has([role=checkbox])]:pr-0"
          : tableCellVariants({ density, kind, tone }),
        className
      )}
      {...props}
    />
  )
}

function TableCaption({
  className,
  ...props
}: React.ComponentProps<"caption">) {
  return (
    <caption
      data-slot="table-caption"
      className={cn("mt-4 text-sm text-muted-foreground", className)}
      {...props}
    />
  )
}

export {
  Table,
  TableHeader,
  TableBody,
  TableFooter,
  TableHead,
  TableRow,
  TableCell,
  TableCaption,
  tableHeadVariants,
  tableCellVariants,
}
