import { resolveMediaUrl } from "@/services/file-storage";

export function serializeReferenceMedia<T extends { url: string; storageKey?: string }>(items: T[] = []): T[] {
    return items.map((item) => ({ ...item, url: item.storageKey ? "" : item.url }));
}

export async function restoreReferenceMedia<T extends { url: string; storageKey?: string }>(items: T[] = []): Promise<T[]> {
    return Promise.all(items.map(async (item) => ({ ...item, url: await resolveMediaUrl(item.storageKey, item.url) })));
}
