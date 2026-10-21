import { useEffect, useRef, type ReactNode } from 'react';
import { X } from 'lucide-react';
import { FLICK_SPEED, useSheetDrag } from '../../hooks/useSheetDrag';
import { useBackToClose } from '../../hooks/useBackToClose';
import { cn } from '@/lib/utils';

interface BottomSheetProps {
    open: boolean;
    onClose: () => void;
    title: ReactNode;
    /** Accessible name when the title is not plain text. */
    label?: string;
    children: ReactNode;
    /** Sticky actions row above the home indicator. */
    footer?: ReactNode;
}

/**
 * Phone bottom sheet: grab handle, swipe down or tap the backdrop to close,
 * Android back closes it. Content height up to 90% of the screen.
 */
export function BottomSheet({ open, onClose, title, label, children, footer }: BottomSheetProps) {
    const sheetRef = useRef<HTMLDivElement>(null);
    const returnFocus = useRef<HTMLElement | null>(null);

    useBackToClose(open, onClose);

    useEffect(() => {
        sheetRef.current?.toggleAttribute('inert', !open);
        if (open) {
            returnFocus.current = document.activeElement as HTMLElement | null;
            sheetRef.current?.focus();
        } else if (returnFocus.current) {
            returnFocus.current.focus?.();
            returnFocus.current = null;
        }
    }, [open]);

    useEffect(() => {
        if (!open) return;
        const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
        document.addEventListener('keydown', onKey);
        return () => document.removeEventListener('keydown', onKey);
    }, [open, onClose]);

    const { dragH, startH, handlers } = useSheetDrag(sheetRef, () => window.innerHeight, ({ height, startHeight, velocity, moved }) => {
        if (moved && (velocity <= -FLICK_SPEED || height < startHeight * 0.6)) onClose();
    });
    const offset = dragH !== null && startH !== null ? Math.max(0, startH - dragH) : 0;

    return (
        <div className={cn('fixed inset-0 z-[1000]', open ? 'visible [transition:none]' : 'invisible [transition:visibility_0s_linear_0.24s] motion-reduce:[transition:none]')} aria-hidden={!open}>
            <div className={cn('absolute inset-0 bg-[rgba(8,11,16,0.6)] [transition:opacity_0.24s_ease] motion-reduce:[transition:none]', open ? 'opacity-100' : 'opacity-0')} onClick={onClose} />
            <div
                ref={sheetRef}
                className={cn(
                    'absolute left-0 right-0 bottom-0 flex flex-col max-h-[90dvh] bg-sg-surface border border-solid border-sg-line border-b-0 rounded-[16px_16px_0_0] shadow-[0_-12px_40px_rgba(0,0,0,0.45)] outline-none',
                    open ? '[transform:translateY(0)]' : '[transform:translateY(100%)]',
                    dragH !== null ? '[transition:none]' : '[transition:transform_0.24s_cubic-bezier(0.2,0.8,0.2,1)] motion-reduce:[transition:none]',
                )}
                style={offset ? { transform: `translateY(${offset}px)` } : undefined}
                role="dialog"
                aria-modal="true"
                aria-label={label ?? (typeof title === 'string' ? title : undefined)}
                tabIndex={-1}
            >
                <div className="shrink-0 touch-none select-none cursor-grab" {...handlers}>
                    <span className="block w-[36px] h-[4px] mt-[8px] mx-auto mb-[4px] rounded-[2px] bg-sg-line" aria-hidden />
                    <div className="flex items-center gap-[8px] pt-[4px] pr-[8px] pb-[10px] pl-[20px] border-b border-b-sg-line-soft">
                        <h2 className="flex-1 min-w-0 m-0 text-sg-text text-[17px] font-semibold">{title}</h2>
                        <button type="button" className="flex items-center justify-center w-[40px] h-[40px] border-0 rounded-pill bg-transparent text-sg-muted cursor-pointer active:bg-sg-hover focus-visible:outline-none focus-visible:shadow-ring" onClick={onClose} aria-label="Close">
                            <X size={18} />
                        </button>
                    </div>
                </div>
                <div className="flex-[1_1_auto] min-h-0 pt-[14px] px-[20px] pb-[16px] overflow-y-auto overscroll-contain last:pb-[calc(16px_+_env(safe-area-inset-bottom))]">{children}</div>
                {footer && <div className="flex gap-[8px] pt-[12px] px-[16px] pb-[calc(12px_+_env(safe-area-inset-bottom))] border-t border-t-sg-line-soft">{footer}</div>}
            </div>
        </div>
    );
}
