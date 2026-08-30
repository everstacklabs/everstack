import dayjs from "dayjs";
import relativeTime from "dayjs/plugin/relativeTime";
import updateLocale from "dayjs/plugin/updateLocale";

// Extend dayjs with relative time plugin
dayjs.extend(relativeTime);
dayjs.extend(updateLocale);

// Customize relative time format: "a few seconds ago" -> "just now", "2 minutes ago" -> "2m ago"
dayjs.updateLocale('en', {
    relativeTime: {
        future: "in %s",
        past: "%s ago",
        s: function (_number: number, _withoutSuffix: boolean) {
            return 'just now'
        },
        ss: '%ds',
        m: '1m',
        mm: '%dm',
        h: '1h',
        hh: '%dh',
        d: '1d',
        dd: '%dd',
        M: '1mo',
        MM: '%dmo',
        y: '1y',
        yy: '%dy'
    }
});

export function formatTimestamp(timestamp: any): string {
    if (!timestamp) return 'N/A';

    // Handle protobuf Timestamp objects with seconds and nanos
    if (typeof timestamp === 'object' && timestamp !== null && 'seconds' in timestamp) {
        // Convert bigint seconds to milliseconds and add nanoseconds
        const milliseconds = Number(timestamp.seconds) * 1000 + (timestamp.nanos || 0) / 1000000;
        return dayjs(milliseconds).format("YYYY-MM-DD HH:mm:ss");
    }

    // Handle Date objects and strings
    return dayjs(timestamp).format("YYYY-MM-DD HH:mm:ss");
}

/**
 * Format timestamp to relative time (e.g., "just now", "2m ago", "5h ago")
 * Handles various timestamp formats including Protobuf Timestamp, Date, number, and string
 */
export function formatRelativeTime(
    timestamp: any
): string {
    if (!timestamp) return 'N/A';

    let date: Date;
    if (typeof timestamp === 'object' && timestamp !== null) {
        if (typeof (timestamp as any).toDate === 'function') {
            // Protobuf timestamp with toDate method
            date = (timestamp as any).toDate();
        } else if ('seconds' in timestamp) {
            // Protobuf timestamp object with seconds and nanos
            const seconds = Number((timestamp as any).seconds || 0);
            const nanos = Number((timestamp as any).nanos || 0);
            date = new Date(seconds * 1000 + Math.floor(nanos / 1000000));
        } else if (timestamp instanceof Date) {
            date = timestamp;
        } else {
            // Regular timestamp
            date = new Date(timestamp as any);
        }
    } else {
        // Handle number or string timestamps
        date = new Date(timestamp);
    }

    const result = dayjs(date).fromNow();
    // Fix "just now ago" -> "just now" (since fromNow() adds the "ago" suffix)
    return result === 'just now ago' ? 'just now' : result;
}