import { useState, useEffect } from 'react';
import { getApiBaseUrl } from '@/lib/api-url';

/**
 * React hook to get the API base URL.
 * This ensures the URL is determined after the component has mounted
 * and window.location is available.
 */
export function useApiBaseUrl(): string {
    const [apiUrl, setApiUrl] = useState(() => {
        // Initial value - try to get it immediately
        return getApiBaseUrl();
    });

    useEffect(() => {
        // Re-evaluate after mount to ensure window.location is available
        const url = getApiBaseUrl();
        if (url !== apiUrl) {
            console.log('[useApiBaseUrl] Updated API URL after mount:', url);
            setApiUrl(url);
        }
    }, [apiUrl]);

    return apiUrl;
}
