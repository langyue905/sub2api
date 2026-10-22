// Provider contract: https://image.kkone.vip/1/docs.html#model-minimax
export const h3Sizes: Record<string, readonly [string, string]> = {
    "16:9": ["1376x768", "1920x1088"],
    "9:16": ["768x1376", "1088x1920"],
    "1:1": ["1024x1024", "1440x1440"],
    "2:3": ["832x1248", "1184x1760"],
    "3:2": ["1248x832", "1760x1184"],
    "3:4": ["896x1184", "1248x1664"],
    "4:3": ["1184x896", "1664x1248"],
    "21:9": ["1568x672", "2208x960"],
};
export const h3Resolutions = [
    { value: "768", label: "768P" }, { value: "1080", label: "1080P" },
    { value: "2k", label: "2K" }, { value: "4k", label: "4K" },
];
export const h3Ratios = Object.keys(h3Sizes).map((value) => {
    const [width, height] = value.split(":").map(Number);
    return { value, width, height };
});
export function isMiniMaxH3(model: string) {
    return model.split("::").pop()?.trim().toLowerCase() === "minimax-h3";
}
export function h3Tier(value: string) {
    const tier = value.trim().toLowerCase().replace(/p$/, "");
    if (!["768", "1080", "2k", "4k"].includes(tier)) throw new Error("MiniMax-H3 清晰度仅支持 768P、1080P、2K、4K。");
    return tier;
}
export function h3Ratio(size: string) {
    if (h3Sizes[size]) return size;
    const ratio = Object.keys(h3Sizes).find((key) => h3Sizes[key].includes(size));
    if (!ratio) throw new Error("请选择 MiniMax-H3 支持的八种画幅之一。");
    return ratio;
}
export function h3Size(tier: string, ratio: string) {
    const normalized = h3Tier(tier);
    if (!h3Sizes[ratio]) throw new Error("MiniMax-H3 画幅不受支持。");
    return normalized === "2k" || normalized === "4k" ? normalized.toUpperCase() : h3Sizes[ratio][normalized === "768" ? 0 : 1];
}
// Persisted image/video settings can contain old or unsupported values. Render
// those as an unselected state; only explicit user selection repairs them.
export function h3Selection(quality: string, size: string) {
    let tier = "", ratio = "", fixedSize = "";
    try { tier = h3Tier(quality); } catch { /* show unselected quality */ }
    try { ratio = h3Ratio(size); } catch { /* show unselected ratio */ }
    if (tier && ratio) fixedSize = h3Size(tier, ratio);
    const valid = Boolean(fixedSize) && (!size.includes("x") || size === fixedSize);
    return { tier, ratio, fixedSize, valid };
}
export function h3VideoOptions(quality: string, size: string, duration: string, prompt: string) {
    const tier = h3Tier(quality);
    const ratio = h3Ratio(size);
    const seconds = Number(duration);
    if (!Number.isInteger(seconds) || seconds < 4 || seconds > 15) throw new Error("MiniMax-H3 时长必须为 4–15 秒的整数。");
    if (!prompt.trim()) throw new Error("请填写视频提示词。");
    // Fixed dimensions belong to a specific tier; never reinterpret them by short side.
    if (size.includes("x") && h3Size(tier, ratio) !== size) throw new Error("MiniMax-H3 尺寸与清晰度冲突，请重新选择画幅。");
    return { seconds, resolution: tier.endsWith("k") ? tier : `${tier}p`, size: h3Size(tier, ratio), ...(tier.endsWith("k") ? { aspect_ratio: ratio } : {}) };
}
export function validateH3References(images: number, videos: number, audios: number) {
    if (images > 9 || videos > 3 || audios > 3) throw new Error("MiniMax-H3 最多支持 9 张图片、3 段视频和 3 段音频。");
    if (!images && (videos || audios)) throw new Error("MiniMax-H3 使用视频或音频参考时，至少需要一张参考图。");
}
