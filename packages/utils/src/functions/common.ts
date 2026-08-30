export function truncateString(str: string, maxLength: number = 10) {
    if (!str || str.length <= maxLength) {
        return str;
    }

    // Special handling for masked API keys (contain many asterisks)
    if (str.includes('*') && str.length > 50) {
        // For masked API keys, show prefix and suffix with ellipsis
        const prefix = str.split('*')[0]; // Get the part before asterisks
        const suffix = str.split('*').pop(); // Get the part after asterisks
        if (prefix && suffix) {
            return `${prefix}...${suffix}`;
        }
    }

    // Default truncation for other strings
    return str.slice(0, maxLength) + '...' + str.slice(-maxLength);
}