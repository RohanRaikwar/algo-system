"use client"

import * as React from "react"
import { Tabs as TabsPrimitive } from "@base-ui/react/tabs"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"

/*
 * Tab looks of the app. TabsList takes the variant and hands it to its
 * triggers through context, so a trigger never needs its own variant prop.
 * The active tab is styled from Base UI's data-active attribute; inactive
 * hover states use not-data-active so the two never compete.
 *
 * Tabs (the root) renders display: contents by default, so wrapping a
 * list and its panels does not add a box (the old markup had the list and
 * the panel as direct children of a flex column). Pass className="flex …"
 * to give it a box.
 */
function Tabs({
  className,
  orientation = "horizontal",
  ...props
}: TabsPrimitive.Root.Props) {
  return (
    <TabsPrimitive.Root
      data-slot="tabs"
      data-orientation={orientation}
      className={cn("group/tabs contents", className)}
      {...props}
    />
  )
}

const tabsListVariants = cva("group/tabs-list", {
  variants: {
    variant: {
      /** shadcn's muted pill list, kept for generic use. */
      default:
        "inline-flex w-fit items-center justify-center rounded-lg p-[3px] text-muted-foreground h-8 bg-muted",
      /** SignalsPage desktop tab bar (nav): surface panel, scrolls sideways, holds tab groups + Separator variant="group". */
      line: "flex items-center gap-[4px] p-[4px] bg-sg-surface border border-solid border-sg-line rounded-panel overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden",
      /** Phone segmented control: SignalsMobile views, HealthPage sections (add flex-1 for full width). */
      segment: "flex p-[3px] gap-[2px] rounded-[10px] bg-sg-surface border border-solid border-sg-line",
      /** Small raised segmented control: DashboardPage Charts / F&O instruments (layoutGroup). */
      compact: "inline-flex p-[2px] gap-[2px] rounded-ctrl bg-sg-raised",
      /** SRSheet inner tabs (S/R strategy / F&O instruments): spaced raised blocks. */
      sheet: "flex gap-[4px] pt-[6px] px-0 pb-[2px]",
    },
  },
  defaultVariants: {
    variant: "default",
  },
})

type TabsListVariant = NonNullable<VariantProps<typeof tabsListVariants>["variant"]>

const TabsListContext = React.createContext<TabsListVariant>("default")

const tabsTriggerVariants = cva(
  "cursor-pointer focus-visible:outline-none focus-visible:shadow-ring",
  {
    variants: {
      variant: {
        /** shadcn trigger, kept for generic use. */
        default:
          "relative inline-flex h-[calc(100%-1px)] flex-1 items-center justify-center gap-1.5 rounded-md border border-transparent px-1.5 py-0.5 text-sm font-medium whitespace-nowrap text-muted-foreground transition-all hover:text-foreground disabled:pointer-events-none disabled:opacity-50 data-active:bg-background data-active:text-foreground data-active:shadow-sm",
        /** SignalsPage desktop tab (`signals-tab-*`): icon + label (+ Badge variant="count-tab"); active = hover bg, inset line, accent icon. */
        line: "inline-flex items-center gap-[7px] py-[8px] px-[12px] border-0 rounded-[10px] text-[0.86rem] font-medium whitespace-nowrap [transition:background_0.15s,color_0.15s] bg-transparent text-sg-muted not-data-active:hover:text-sg-text not-data-active:hover:bg-sg-raised data-active:text-sg-text data-active:bg-sg-hover data-active:shadow-[inset_0_0_0_1px_var(--sg-line)] data-active:focus-visible:shadow-[inset_0_0_0_1px_var(--sg-line)] data-active:[&_svg]:text-sg-accent",
        /** Phone segment (SignalsMobile, HealthPage): 34px, 13px; active semibold with inset line. */
        segment:
          "flex-1 inline-flex items-center justify-center gap-[5px] h-[34px] min-w-0 py-0 px-[4px] border-0 rounded-[7px] text-[13px] [-webkit-tap-highlight-color:transparent] select-none active:bg-sg-hover bg-transparent text-sg-muted font-medium data-active:bg-sg-hover data-active:text-sg-text data-active:font-semibold data-active:shadow-[inset_0_0_0_1px_var(--sg-line)] data-active:focus-visible:shadow-ring",
        /** DashboardPage view switch button (layoutBtn): 12px semibold. */
        compact:
          "min-w-[26px] py-[2px] px-[8px] border-0 rounded-[6px] text-[12px] font-semibold hover:text-sg-text bg-transparent text-sg-muted data-active:bg-sg-hover data-active:text-sg-text",
        /** SRSheet inner tab: equal-width raised block, min 36px. */
        sheet:
          "flex-[1_1_0] min-h-[36px] border-0 rounded-[8px] text-[13px] font-semibold bg-sg-raised text-sg-muted data-active:bg-sg-hover data-active:text-sg-text",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)

function TabsList({
  className,
  variant = "default",
  ...props
}: TabsPrimitive.List.Props & VariantProps<typeof tabsListVariants>) {
  const v = variant ?? "default"
  return (
    <TabsListContext.Provider value={v}>
      <TabsPrimitive.List
        data-slot="tabs-list"
        data-variant={v}
        className={cn(tabsListVariants({ variant: v }), className)}
        {...props}
      />
    </TabsListContext.Provider>
  )
}

function TabsTrigger({ className, ...props }: TabsPrimitive.Tab.Props) {
  const variant = React.useContext(TabsListContext)
  return (
    <TabsPrimitive.Tab
      data-slot="tabs-trigger"
      className={
        typeof className === "function"
          ? (state) => cn(tabsTriggerVariants({ variant }), className(state))
          : cn(tabsTriggerVariants({ variant }), className)
      }
      {...props}
    />
  )
}

/** No styling of its own: the old panels carried their own classes. */
function TabsContent({ className, ...props }: TabsPrimitive.Panel.Props) {
  return (
    <TabsPrimitive.Panel
      data-slot="tabs-content"
      className={className}
      {...props}
    />
  )
}

export {
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
  tabsListVariants,
  tabsTriggerVariants,
}
