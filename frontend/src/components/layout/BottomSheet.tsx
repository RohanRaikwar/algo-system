import type { ReactNode } from 'react';
import { X } from 'lucide-react';
import { useBackToClose } from '../../hooks/useBackToClose';
import { Button } from '@/components/ui/button';
import {
    Drawer,
    DrawerBody,
    DrawerClose,
    DrawerContent,
    DrawerFooter,
    DrawerHeader,
    DrawerTitle,
} from '@/components/ui/drawer';

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
 * Swipe, backdrop, Esc and the close button all end in onClose, so the
 * history entry pushed by useBackToClose is dropped the same way.
 */
export function BottomSheet({ open, onClose, title, label, children, footer }: BottomSheetProps) {
    useBackToClose(open, onClose);

    return (
        <Drawer open={open} onOpenChange={(next) => { if (!next) onClose(); }} swipeDirection="down">
            <DrawerContent
                {...(label ? { 'aria-label': label, 'aria-labelledby': undefined } : {})}
            >
                <DrawerHeader className="touch-none select-none cursor-grab">
                    <DrawerTitle>{title}</DrawerTitle>
                    <DrawerClose render={<Button variant="ghost-touch" size="icon-40" aria-label="Close" />}>
                        <X size={18} />
                    </DrawerClose>
                </DrawerHeader>
                <DrawerBody>{children}</DrawerBody>
                {footer && <DrawerFooter>{footer}</DrawerFooter>}
            </DrawerContent>
        </Drawer>
    );
}
