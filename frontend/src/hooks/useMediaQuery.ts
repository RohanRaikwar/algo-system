import { useEffect, useState } from 'react';

/** Phone breakpoint shared by CSS modules (`max-width: 640px`). */
export const PHONE_QUERY = '(max-width: 640px)';

function matches(query: string): boolean {
    return typeof window !== 'undefined' && typeof window.matchMedia === 'function'
        && window.matchMedia(query).matches;
}

/** Live media-query match; updates on resize and rotation. */
export function useMediaQuery(query: string): boolean {
    const [match, setMatch] = useState(() => matches(query));

    useEffect(() => {
        if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return;
        const mql = window.matchMedia(query);
        const onChange = () => setMatch(mql.matches);
        onChange();
        mql.addEventListener('change', onChange);
        return () => mql.removeEventListener('change', onChange);
    }, [query]);

    return match;
}

/** Phone held sideways: wider than PHONE_QUERY but too short for the desktop layout. */
export const PHONE_LANDSCAPE_QUERY = '(max-width: 940px) and (max-height: 500px) and (orientation: landscape)';
