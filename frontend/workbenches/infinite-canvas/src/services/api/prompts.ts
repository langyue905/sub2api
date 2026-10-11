export type Prompt = {
    id: string;
    sourceId: string;
    category: string;
    title: string;
    prompt: string;
    description: string;
    coverUrl: string;
    referenceImageUrls: string[];
    tags: string[];
    preview: string;
    createdAt: string;
    updatedAt: string;
};

export const ALL_PROMPTS_OPTION = "all";

export type PromptListResponse = {
    items: Prompt[];
    tags: string[];
    categories: string[];
    total: number;
};

const LOCAL_PROMPT_DATE = "2026-10-11T00:00:00.000Z";

const LOCAL_PROMPTS: Prompt[] = [
    { id: "cinematic-portrait", sourceId: "built-in", category: "人物", title: "电影感人像", prompt: "A cinematic editorial portrait of [subject], [wardrobe], standing in [location], soft directional key light, subtle rim light, natural skin texture, 85mm lens, shallow depth of field, restrained color grading, highly detailed.", description: "适合人物写真和角色设定，可替换方括号内的内容。", coverUrl: "", referenceImageUrls: [], tags: ["人像", "电影感", "摄影"], preview: "[subject] / [wardrobe] / [location]", createdAt: LOCAL_PROMPT_DATE, updatedAt: LOCAL_PROMPT_DATE },
    { id: "product-studio", sourceId: "built-in", category: "产品", title: "产品棚拍", prompt: "Premium studio product photography of [product], centered on a clean [background color] backdrop, softbox lighting, crisp material details, realistic reflections, elegant composition, commercial advertising quality, no text, no watermark.", description: "适合电商商品图、产品概念图和主视觉。", coverUrl: "", referenceImageUrls: [], tags: ["产品", "电商", "棚拍"], preview: "[product] / [background color]", createdAt: LOCAL_PROMPT_DATE, updatedAt: LOCAL_PROMPT_DATE },
    { id: "architecture-interior", sourceId: "built-in", category: "空间", title: "现代室内设计", prompt: "Wide-angle architectural interior photograph of a [style] [room type], thoughtful furniture layout, natural daylight through large windows, tactile materials, balanced composition, realistic scale, magazine photography, no people.", description: "用于家居、酒店、办公空间等室内效果图。", coverUrl: "", referenceImageUrls: [], tags: ["室内", "建筑", "设计"], preview: "[style] / [room type]", createdAt: LOCAL_PROMPT_DATE, updatedAt: LOCAL_PROMPT_DATE },
    { id: "fantasy-landscape", sourceId: "built-in", category: "场景", title: "奇幻场景", prompt: "Epic fantasy landscape of [place], dramatic atmosphere, layered foreground and distant mountains, volumetric sunlight, intricate environmental storytelling, cinematic scale, rich natural detail, concept art.", description: "适合游戏场景、世界观概念和海报背景。", coverUrl: "", referenceImageUrls: [], tags: ["场景", "奇幻", "概念设计"], preview: "[place]", createdAt: LOCAL_PROMPT_DATE, updatedAt: LOCAL_PROMPT_DATE },
    { id: "minimal-poster", sourceId: "built-in", category: "平面", title: "极简海报", prompt: "A minimal graphic poster for [theme], bold geometric shapes, deliberate negative space, a focused [color palette] palette, clean print texture, contemporary Swiss-inspired composition, no readable text.", description: "用于活动主视觉、封面和社交媒体配图。", coverUrl: "", referenceImageUrls: [], tags: ["海报", "平面", "极简"], preview: "[theme] / [color palette]", createdAt: LOCAL_PROMPT_DATE, updatedAt: LOCAL_PROMPT_DATE },
    { id: "food-editorial", sourceId: "built-in", category: "产品", title: "美食编辑摄影", prompt: "Editorial food photography of [dish] on [table setting], appetizing texture and steam, warm natural side light, carefully styled ingredients, shallow depth of field, refined restaurant magazine aesthetic.", description: "用于餐饮菜单、社交平台和品牌内容。", coverUrl: "", referenceImageUrls: [], tags: ["美食", "摄影", "餐饮"], preview: "[dish] / [table setting]", createdAt: LOCAL_PROMPT_DATE, updatedAt: LOCAL_PROMPT_DATE },
    { id: "character-sheet", sourceId: "built-in", category: "人物", title: "角色设定三视图", prompt: "Professional character design sheet for [character], front, side, and back views, consistent costume details, clean neutral background, full body, expressive face studies, polished game concept art.", description: "用于角色设计、服装设定和游戏开发参考。", coverUrl: "", referenceImageUrls: [], tags: ["角色", "设定", "游戏"], preview: "[character]", createdAt: LOCAL_PROMPT_DATE, updatedAt: LOCAL_PROMPT_DATE },
    { id: "short-video-storyboard", sourceId: "built-in", category: "视频", title: "短片分镜提示词", prompt: "A [seconds]-second cinematic shot of [subject] [action] in [location]. Camera: [camera movement]. Lighting: [lighting]. Mood: [mood]. Keep the subject consistent, realistic motion, detailed environment, no captions or watermark.", description: "可直接用于视频生成模型，替换时长、主体、动作和运镜。", coverUrl: "", referenceImageUrls: [], tags: ["视频", "分镜", "运镜"], preview: "[seconds] / [subject] / [action] / [camera movement]", createdAt: LOCAL_PROMPT_DATE, updatedAt: LOCAL_PROMPT_DATE },
];

export async function fetchPrompts({ keyword = "", tag = [], category = ALL_PROMPTS_OPTION, page = 1, pageSize = 20 }: { keyword?: string; tag?: string[]; category?: string; page?: number; pageSize?: number } = {}): Promise<PromptListResponse> {
    const normalizedKeyword = keyword.trim().toLowerCase();
    const normalizedPage = Math.max(1, page);
    const normalizedPageSize = Math.max(1, Math.min(100, pageSize));
    const withoutTagFilter = filterPrompts(LOCAL_PROMPTS, { keyword: normalizedKeyword, category, tags: [] });
    const filtered = filterPrompts(LOCAL_PROMPTS, { keyword: normalizedKeyword, category, tags: tag });
    return { items: filtered.slice((normalizedPage - 1) * normalizedPageSize, normalizedPage * normalizedPageSize), tags: collectTags(withoutTagFilter), categories: Array.from(new Set(LOCAL_PROMPTS.map((item) => item.category))), total: filtered.length };
}

function filterPrompts(items: Prompt[], options: { keyword: string; category: string; tags: string[] }) {
    return items.filter((item) => {
        if (isActiveOption(options.category) && item.category !== options.category) return false;
        if (options.tags.length && !options.tags.some((tag) => item.tags.includes(tag))) return false;
        return !options.keyword || [item.title, item.prompt, item.description, item.category, ...item.tags].join(" ").toLowerCase().includes(options.keyword);
    });
}

function collectTags(items: Prompt[]) {
    return Array.from(new Set(items.flatMap((item) => item.tags).filter(Boolean)));
}

function isActiveOption(value: string) {
    return value && value !== ALL_PROMPTS_OPTION;
}

export function formatPromptDate(value: string, locale?: string) {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? "" : new Intl.DateTimeFormat(locale, { year: "numeric", month: "2-digit", day: "2-digit" }).format(date);
}
