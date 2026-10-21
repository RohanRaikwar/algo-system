/*
 * Dialog, styled as the app's SettingsModal by default: backdrop
 * rgba(8,11,16,0.6), 540px surface panel with a line border, 16px radius,
 * 0 24px 64px shadow, max height min(86vh,720px), fading in from 8px lower
 * over 0.18s; at <=640px it sits at the bottom, full width, top-rounded.
 * Header / Body / Footer carry the modal's paddings and line-soft rules.
 * No close button by default (the app's dialogs put theirs in the header:
 * Button variant="ghost" size="icon-32" with <X size={18} />).
 */
"use client"

import * as React from "react"
import { Dialog as DialogPrimitive } from "@base-ui/react/dialog"
import { cn } from "cn"

import { Button } from "@/components/ui/button"
import { XIcon } from "lucide-react"

function Dialog({ ...props }: DialogPrimitive.Root.Props) {
  return <DialogPrimitive.Root data-slot="dialog" {...props} />
}

function DialogTrigger({ ...props }: DialogPrimitive.Trigger.Props) {
  return <DialogPrimitive.Trigger data-slot="dialog-trigger" {...props} />
}

function DialogPortal({ ...props }: DialogPrimitive.Portal.Props) {
  return <DialogPrimitive.Portal data-slot="dialog-portal" {...props} />
}

function DialogClose({ ...props }: DialogPrimitive.Close.Props) {
  return <DialogPrimitive.Close data-slot="dialog-close" {...props} />
}

function DialogOverlay({
  className,
  ...props
}: DialogPrimitive.Backdrop.Props) {
  return (
    <DialogPrimitive.Backdrop
      data-slot="dialog-overlay"
      className={cn(
        "fixed inset-0 isolate z-50 bg-[rgba(8,11,16,0.6)] motion-safe:[transition:opacity_0.18s_ease] data-starting-style:opacity-0 data-ending-style:opacity-0",
        className
      )}
      {...props}
    />
  )
}

function DialogContent({
  className,
  children,
  showCloseButton = false,
  ...props
}: DialogPrimitive.Popup.Props & {
  showCloseButton?: boolean
}) {
  return (
    <DialogPortal>
      <DialogOverlay />
      <DialogPrimitive.Popup
        data-slot="dialog-content"
        className={cn(
          "fixed top-1/2 left-1/2 z-50 flex flex-col w-[540px] max-w-[calc(100%_-_32px)] max-h-[min(86vh,720px)] -translate-x-1/2 -translate-y-1/2 bg-sg-surface text-sg-text border border-solid border-sg-line rounded-[16px] shadow-[0_24px_64px_rgba(0,0,0,0.5)] outline-none motion-safe:[transition:opacity_0.18s_ease,translate_0.18s_ease] data-starting-style:opacity-0 data-starting-style:translate-y-[calc(-50%_+_8px)] data-ending-style:opacity-0 data-ending-style:translate-y-[calc(-50%_+_8px)] lte-640:top-auto lte-640:bottom-0 lte-640:left-0 lte-640:translate-x-0 lte-640:translate-y-0 lte-640:w-full lte-640:max-w-full lte-640:max-h-[90dvh] lte-640:rounded-[16px_16px_0_0] [&_button:focus-visible]:outline-none [&_button:focus-visible]:shadow-ring",
          className
        )}
        {...props}
      >
        {children}
        {showCloseButton && (
          <DialogPrimitive.Close
            data-slot="dialog-close"
            render={
              <Button
                variant="ghost"
                className="absolute top-[18px] right-[20px]"
                size="icon-32"
              />
            }
          >
            <XIcon size={18} />
            <span className="sr-only">Close</span>
          </DialogPrimitive.Close>
        )}
      </DialogPrimitive.Popup>
    </DialogPortal>
  )
}

function DialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-header"
      className={cn(
        "flex items-start justify-between gap-[12px] pt-[18px] px-[20px] pb-[14px] border-b border-b-sg-line-soft",
        className
      )}
      {...props}
    />
  )
}

function DialogFooter({
  className,
  showCloseButton = false,
  children,
  ...props
}: React.ComponentProps<"div"> & {
  showCloseButton?: boolean
}) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn(
        "flex items-center gap-[8px] py-[14px] px-[20px] border-t border-t-sg-line-soft",
        className
      )}
      {...props}
    >
      {children}
      {showCloseButton && (
        <DialogPrimitive.Close render={<Button variant="secondary" size="lg" />}>
          Close
        </DialogPrimitive.Close>
      )}
    </div>
  )
}

/** SettingsModal scroll body: 18/20/20 padding, 22px between sections. */
function DialogBody({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-body"
      className={cn(
        "flex flex-col gap-[22px] pt-[18px] px-[20px] pb-[20px] overflow-y-auto",
        className
      )}
      {...props}
    />
  )
}

function DialogTitle({ className, ...props }: DialogPrimitive.Title.Props) {
  return (
    <DialogPrimitive.Title
      data-slot="dialog-title"
      className={cn(
        "text-sg-text text-[1.1rem] font-semibold",
        className
      )}
      {...props}
    />
  )
}

function DialogDescription({
  className,
  ...props
}: DialogPrimitive.Description.Props) {
  return (
    <DialogPrimitive.Description
      data-slot="dialog-description"
      className={cn(
        "mt-[2px] text-sg-muted text-[0.84rem]",
        className
      )}
      {...props}
    />
  )
}

export {
  Dialog,
  DialogBody,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogOverlay,
  DialogPortal,
  DialogTitle,
  DialogTrigger,
}
