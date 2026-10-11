const MODEL_MEDIA_ORIGIN = "https://api.zshai.cc";

// Generated media may be fetched from the locked API gateway; local browser URLs stay local.
export function isAllowedMediaDownloadUrl(value: string) {
    if (value.startsWith("data:") || value.startsWith("blob:")) return true;
    try {
        const url = new URL(value, window.location.origin);
        return url.origin === window.location.origin || url.origin === MODEL_MEDIA_ORIGIN;
    } catch {
        return false;
    }
}

export function assertAllowedMediaDownloadUrl(value: string) {
    if (!isAllowedMediaDownloadUrl(value)) throw new Error("This media URL is not allowed for download.");
}
