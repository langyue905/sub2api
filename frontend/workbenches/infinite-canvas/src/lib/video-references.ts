import type { ReferenceImage } from "@/types/image";

export function normalizeVideoMode(mode?: string) {
    return mode === "first_last_frame" ? mode : mode === "reference" ? mode : "frames";
}

export function selectVideoFrameImages(images: ReferenceImage[], mode?: string, firstFrameId?: string, lastFrameId?: string) {
    if (mode !== "first_last_frame") return images;
    const first = images.find((image) => image.id === firstFrameId);
    const last = images.find((image) => image.id === lastFrameId);
    if (!first || !last) throw new Error("请明确选择首帧和尾帧图片；素材已移除时请重新选择。");
    return [first, last];
}
