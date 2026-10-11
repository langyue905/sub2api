import { useMemo } from "react";
import { create } from "zustand";
import { persist } from "zustand/middleware";

export type ApiCallFormat = "openai" | "gemini";
export type ModelCapability = "image" | "video" | "text" | "audio";
export type ReasoningEffort = "auto" | "low" | "medium" | "high" | "xhigh";
export type ChannelModel = { name: string; capability: ModelCapability };
export type ModelChannel = { id: string; name: string; baseUrl: string; apiKey: string; apiFormat: ApiCallFormat; models: ChannelModel[] };
export type ChannelCredentialsImportResult = { status: "created" | "updated" | "missing-base-url" | "invalid-base-url"; channelName?: string };

export type AiConfig = {
    channelMode: "remote" | "local"; baseUrl: string; apiKey: string; apiFormat: ApiCallFormat; channels: ModelChannel[];
    model: string; imageModel: string; videoModel: string; textModel: string; audioModel: string;
    audioVoice: string; audioFormat: string; audioSpeed: string; audioInstructions: string;
    videoSeconds: string; vquality: string; videoGenerateAudio: string; videoWatermark: string; videoMode: string;
    systemPrompt: string; reasoningEffort: ReasoningEffort; models: string[]; quality: string; size: string; background: string;
    count: string; canvasImageCount: string;
    nodeRegenerateBehavior: "new-node" | "overwrite"; autoFocusOnSelect: boolean;
};

export const CONFIG_STORE_KEY = "infinite-canvas:ai_config_store";
export const LOCKED_API_BASE_URL = "https://api.zshai.cc";
const LOCKED_CHANNEL_ID = "zshai";
const LOCKED_CHANNEL_NAME = "ZSH AI";
const CHANNEL_MODEL_SEPARATOR = "::";
const seedModels: ChannelModel[] = [
    { name: "gpt-image-2", capability: "image" }, { name: "grok-imagine-video", capability: "video" },
    { name: "gpt-5.5", capability: "text" }, { name: "gpt-4o-mini-tts", capability: "audio" },
];

export const defaultConfig: AiConfig = {
    channelMode: "local", baseUrl: LOCKED_API_BASE_URL, apiKey: "", apiFormat: "openai",
    channels: [{ id: LOCKED_CHANNEL_ID, name: LOCKED_CHANNEL_NAME, baseUrl: LOCKED_API_BASE_URL, apiKey: "", apiFormat: "openai", models: seedModels }],
    model: `${LOCKED_CHANNEL_ID}::gpt-image-2`, imageModel: `${LOCKED_CHANNEL_ID}::gpt-image-2`, videoModel: `${LOCKED_CHANNEL_ID}::grok-imagine-video`, textModel: `${LOCKED_CHANNEL_ID}::gpt-5.5`, audioModel: `${LOCKED_CHANNEL_ID}::gpt-4o-mini-tts`,
    audioVoice: "alloy", audioFormat: "mp3", audioSpeed: "1", audioInstructions: "", videoSeconds: "6", vquality: "720", videoGenerateAudio: "true", videoWatermark: "false", videoMode: "frames",
    systemPrompt: "", reasoningEffort: "auto", models: seedModels.map((model) => encodeChannelModel(LOCKED_CHANNEL_ID, model.name)), quality: "auto", size: "1:1", background: "", count: "1", canvasImageCount: "3",
    nodeRegenerateBehavior: "new-node", autoFocusOnSelect: false,
};

type ConfigStore = {
    config: AiConfig; isConfigOpen: boolean; shouldPromptContinue: boolean;
    updateConfig: <K extends keyof AiConfig>(key: K, value: AiConfig[K]) => void;
    importChannelCredentials: (input: { baseUrl?: string | null; apiKey?: string | null }) => ChannelCredentialsImportResult;
    isAiConfigReady: (config: AiConfig, model: string) => boolean;
    openConfigDialog: (shouldPromptContinue?: boolean) => void;
    setConfigDialogOpen: (isOpen: boolean) => void; clearPromptContinue: () => void;
};

const VIDEO_KEYWORDS = ["video", "sora", "veo", "kling", "wan", "hailuo"];
const AUDIO_KEYWORDS = ["audio", "tts", "speech", "voice", "music", "sound"];
const IMAGE_KEYWORDS = ["seedream", "gpt-image", "image", "dall-e", "dalle", "imagen", "flux", "sdxl", "stable-diffusion", "midjourney"];
export function boolConfig(value: string, fallback: boolean) { return value ? value === "true" : fallback; }
export function guessCapability(name: string): ModelCapability {
    const value = name.toLowerCase();
    if (VIDEO_KEYWORDS.some((keyword) => value.includes(keyword))) return "video";
    if (AUDIO_KEYWORDS.some((keyword) => value.includes(keyword))) return "audio";
    return IMAGE_KEYWORDS.some((keyword) => value.includes(keyword)) ? "image" : "text";
}
export function encodeChannelModel(channelId: string, model: string) { return `${channelId}${CHANNEL_MODEL_SEPARATOR}${model.trim()}`; }
export function isChannelModelValue(value: string) { return value.includes(CHANNEL_MODEL_SEPARATOR); }
export function decodeChannelModel(value: string) { const index = value.indexOf(CHANNEL_MODEL_SEPARATOR); return index < 0 ? null : { channelId: value.slice(0, index), model: value.slice(index + CHANNEL_MODEL_SEPARATOR.length) }; }
export function modelOptionName(value: string) { return decodeChannelModel(value)?.model || value; }
export function normalizeChannelModels(models: Array<string | ChannelModel> | undefined): ChannelModel[] {
    const seen = new Set<string>();
    return (models || []).flatMap((value) => {
        const name = (typeof value === "string" ? modelOptionName(value) : value?.name || "").trim();
        if (!name || seen.has(name)) return [];
        seen.add(name);
        return [{ name, capability: typeof value === "string" ? guessCapability(name) : value.capability || guessCapability(name) }];
    });
}
function allModels(input: Partial<AiConfig>) {
    if (input.channels !== undefined) {
        return normalizeChannelModels(input.channels.flatMap((channel) => channel.models || []));
    }
    return normalizeChannelModels([
        ...seedModels,
        ...((input.models || []).map((model) => ({ name: modelOptionName(model), capability: guessCapability(modelOptionName(model)) }))),
        ...[input.model, input.imageModel, input.videoModel, input.textModel, input.audioModel].filter(Boolean).map((model) => ({ name: modelOptionName(model || ""), capability: guessCapability(modelOptionName(model || "")) })),
    ]);
}
function optionFor(models: ChannelModel[], value: string | undefined, capability: ModelCapability, fallback: string) {
    const name = modelOptionName(value || "");
    const selected = models.find((model) => model.name === name && model.capability === capability) || models.find((model) => model.capability === capability && model.name === modelOptionName(fallback)) || models.find((model) => model.capability === capability);
    return selected ? encodeChannelModel(LOCKED_CHANNEL_ID, selected.name) : "";
}
function normalizeLockedConfig(input: Partial<AiConfig>): AiConfig {
    const models = allModels(input);
    const apiKey = input.apiKey !== undefined ? input.apiKey.trim() : input.channels?.map((channel) => channel.apiKey?.trim()).find(Boolean) || "";
    const channel: ModelChannel = { id: LOCKED_CHANNEL_ID, name: LOCKED_CHANNEL_NAME, baseUrl: LOCKED_API_BASE_URL, apiKey, apiFormat: "openai", models };
    const base = { ...defaultConfig, ...input } as AiConfig;
    return {
        ...base, channelMode: "local", baseUrl: LOCKED_API_BASE_URL, apiKey, apiFormat: "openai", channels: [channel], models: models.map((model) => encodeChannelModel(LOCKED_CHANNEL_ID, model.name)),
        model: optionFor(models, input.model || input.imageModel, "image", defaultConfig.imageModel), imageModel: optionFor(models, input.imageModel || input.model, "image", defaultConfig.imageModel), videoModel: optionFor(models, input.videoModel, "video", defaultConfig.videoModel), textModel: optionFor(models, input.textModel || input.model, "text", defaultConfig.textModel), audioModel: optionFor(models, input.audioModel, "audio", defaultConfig.audioModel),
        audioVoice: input.audioVoice || defaultConfig.audioVoice, audioFormat: input.audioFormat || defaultConfig.audioFormat, audioSpeed: input.audioSpeed || defaultConfig.audioSpeed, audioInstructions: input.audioInstructions || "", videoSeconds: input.videoSeconds || defaultConfig.videoSeconds, vquality: input.vquality || defaultConfig.vquality, videoGenerateAudio: input.videoGenerateAudio || defaultConfig.videoGenerateAudio, videoWatermark: input.videoWatermark || defaultConfig.videoWatermark, videoMode: input.videoMode === "reference" ? "reference" : "frames", canvasImageCount: input.canvasImageCount || defaultConfig.canvasImageCount, reasoningEffort: input.reasoningEffort || "auto", nodeRegenerateBehavior: input.nodeRegenerateBehavior === "overwrite" ? "overwrite" : "new-node", autoFocusOnSelect: Boolean(input.autoFocusOnSelect),
    };
}

export const useConfigStore = create<ConfigStore>()(persist((set) => ({
    config: defaultConfig, isConfigOpen: false, shouldPromptContinue: false,
    updateConfig: (key, value) => set((state) => ({ config: normalizeLockedConfig({ ...state.config, [key]: value }) })),
    importChannelCredentials: (input) => {
        const apiKey = input.apiKey?.trim();
        if (!apiKey) return { status: "missing-base-url" };
        set((state) => ({ config: normalizeLockedConfig({ ...state.config, apiKey }) }));
        return { status: "updated", channelName: LOCKED_CHANNEL_NAME };
    },
    isAiConfigReady: (config, model) => Boolean(modelOptionName(model).trim() && config.apiKey.trim()),
    openConfigDialog: (shouldPromptContinue = false) => set({ isConfigOpen: true, shouldPromptContinue }),
    setConfigDialogOpen: (isConfigOpen) => set({ isConfigOpen }), clearPromptContinue: () => set({ shouldPromptContinue: false }),
}), { name: CONFIG_STORE_KEY, partialize: (state) => ({ config: state.config }), merge: (persisted, current) => ({ ...current, config: normalizeLockedConfig(((persisted || {}) as { config?: Partial<AiConfig> }).config || {}) }) }));

export function useEffectiveConfig() { const config = useConfigStore((state) => state.config); return useMemo(() => ({ ...config, channelMode: "local" as const, baseUrl: LOCKED_API_BASE_URL, apiFormat: "openai" as const }), [config]); }
export function modelCapabilityOf(config: AiConfig, value: string) { return config.channels[0]?.models.find((model) => model.name === modelOptionName(value))?.capability; }
export function modelMatchesCapability(config: AiConfig, value: string, capability?: ModelCapability) { return !capability || modelCapabilityOf(config, value) === capability; }
export function selectableModelsByCapability(config: AiConfig, capability?: ModelCapability) { return config.channels[0]?.models.filter((model) => !capability || model.capability === capability).map((model) => encodeChannelModel(LOCKED_CHANNEL_ID, model.name)) || []; }
export function resolveModelForCapability(config: AiConfig, currentModel: string | undefined, capability: ModelCapability) { const candidate = [currentModel, capability === "image" ? config.imageModel : capability === "video" ? config.videoModel : capability === "audio" ? config.audioModel : config.textModel].find((model) => modelCapabilityOf(config, model || "") === capability); return candidate || selectableModelsByCapability(config, capability)[0] || ""; }
export function modelOptionLabel(_config: AiConfig, value: string) { return modelOptionName(value); }
export function modelOptionsFromChannels(channels: ModelChannel[]) { return channels.flatMap((channel) => channel.models.map((model) => encodeChannelModel(LOCKED_CHANNEL_ID, model.name))); }
export function normalizeModelOptionValue(value: string | undefined, _channels: ModelChannel[]) { return value ? encodeChannelModel(LOCKED_CHANNEL_ID, modelOptionName(value)) : ""; }
export function createModelChannel(channel?: Partial<ModelChannel>): ModelChannel { return { id: LOCKED_CHANNEL_ID, name: LOCKED_CHANNEL_NAME, baseUrl: LOCKED_API_BASE_URL, apiKey: channel?.apiKey || "", apiFormat: "openai", models: normalizeChannelModels(channel?.models) }; }
export function upsertChannelCredentials(config: AiConfig, input: { baseUrl?: string | null; apiKey?: string | null }): ChannelCredentialsImportResult & { config: AiConfig } { const apiKey = input.apiKey?.trim(); return apiKey ? { status: "updated", channelName: LOCKED_CHANNEL_NAME, config: normalizeLockedConfig({ ...config, apiKey }) } : { status: "missing-base-url", config }; }
export function resolveModelChannel(config: AiConfig, _value: string) { return config.channels[0] || defaultConfig.channels[0]; }
export function resolveModelRequestConfig(config: AiConfig, value: string) { return { ...normalizeLockedConfig(config), model: modelOptionName(value || config.model), baseUrl: LOCKED_API_BASE_URL, apiFormat: "openai" as const }; }
export function defaultBaseUrlForApiFormat(_apiFormat: ApiCallFormat) { return LOCKED_API_BASE_URL; }
export function buildApiUrl(_baseUrl: string, path: string) { return `${LOCKED_API_BASE_URL}/v1${path.startsWith("/") ? path : `/${path}`}`; }
