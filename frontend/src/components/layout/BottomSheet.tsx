import { useEffect, useRef, type ReactNode } from 'react';
import { X } from 'lucide-react';
import { FLICK_SPEED, useSheetDrag } from '../../hooks/useSheetDrag';
import { useBackToClose } from '../../hooks/useBackToClose';
import styles from './BottomSheet.module.css';

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
        <div className={`${styles.root}${open ? ` ${styles.open}` : ''}`} aria-hidden={!open}>
            <div className={styles.backdrop} onClick={onClose} />
            <div
                ref={sheetRef}
                className={`${styles.sheet}${dragH !== null ? ` ${styles.dragging}` : ''}`}
                style={offset ? { transform: `translateY(${offset}px)` } : undefined}
                role="dialog"
                aria-modal="true"
                aria-label={label ?? (typeof title === 'string' ? title : undefined)}
                tabIndex={-1}
            >
                <div className={styles.head} {...handlers}>
                    <span className={styles.handle} aria-hidden />
                    <div className={styles.titleRow}>
                        <h2 className={styles.title}>{title}</h2>
                        <button type="button" className={styles.close} onClick={onClose} aria-label="Close">
                            <X size={18} />
                        </button>
                    </div>
                </div>
                <div className={styles.body}>{children}</div>
                {footer && <div className={styles.footer}>{footer}</div>}
            </div>
        </div>
    );
}
