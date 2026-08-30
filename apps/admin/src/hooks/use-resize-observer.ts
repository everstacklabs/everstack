import { useEffect, useState, type RefObject } from "react";

export function useResizeObserver(targetRef: RefObject<Element>) {
    const [entry, setEntry] = useState<ResizeObserverEntry | null>(null);

    useEffect(() => {
        const node = targetRef.current;
        if (!node) return;

        if (typeof window === "undefined" || typeof ResizeObserver === "undefined") {
            return;
        }

        const observer = new ResizeObserver((entries) => {
            if (entries[0]) {
                setEntry(entries[0]);
            }
        });

        observer.observe(node);
        return () => observer.disconnect();
    }, [targetRef]);

    return entry;
}


