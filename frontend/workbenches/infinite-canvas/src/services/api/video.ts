import axios from "axios";

import i18n from "@/i18n";
import { clampVideoSeconds, inferVideoRatio, parseVideoResolution } from "@/lib/media-size";
import { getMediaBlob, uploadMediaFile, type UploadedFile } from "@/services/file-storage";
import { getImageBlob, imageToDataUrl } from "@/services/image-storage";
import { assertAllowedMediaDownloadUrl } from "@/services/media-url-policy";
import { buildApiUrl, modelOptionName, resolveModelRequestConfig, type AiConfig } from "@/stores/use-config-store";
import type { ReferenceImage } from "@/types/image";
import type { ReferenceAudio, ReferenceVideo } from "@/types/media";

type VideoResponse = {
    id?: string;
    request_id?: string;
    task_id?: string;
    status?: string;
    error?: { message?: string } | string;
    url?: string;
    result_url?: string;
    video_url?: string;
    content?: { video_url?: string; url?: string } | null;
    video?: VideoResponse;
    data?: VideoResponse | null;
};
type ApiVideoResponse = VideoResponse | { code?: number | string; data?: VideoResponse | null; msg?: string; message?: string; error?: { message?: string } };
type ApiEnvelope<T> = T | { code?: number | string; data?: T | null; msg?: string; message?: string; error?: { message?: string } };
type RequestOptions = { signal?: AbortSignal };
type VideoMediaOptions = RequestOptions & { videos?: ReferenceVideo[]; audios?: ReferenceAudio[] };
const apiText = (key: string, options?: Record<string, unknown>) => i18n.t(`apiErrors.${key}`, options);

export type VideoGenerationResult = { blob?: Blob; url?: string; mimeType?: string };
export type VideoGenerationTask = { id: string; provider: "openai"; model: string };
export type VideoGenerationTaskState = { status: "pending" } | { status: "completed"; result: VideoGenerationResult } | { status: "failed"; error: string };


function aiApiUrl(config: AiConfig, path: string) {
    return buildApiUrl(config.baseUrl, path);
}

function aiHeaders(config: AiConfig, contentType?: string) {
    return {
        Authorization: `Bearer ${config.apiKey}`,
        ...(contentType ? { "Content-Type": contentType } : {}),
    };
}

export async function requestVideoGeneration(config: AiConfig, prompt: string, references: ReferenceImage[] = [], options?: VideoMediaOptions): Promise<VideoGenerationResult> {
    return waitForVideoGenerationTask(config, await createVideoGenerationTask(config, prompt, references, options), options);
}

export async function waitForVideoGenerationTask(config: AiConfig, task: VideoGenerationTask, options?: RequestOptions): Promise<VideoGenerationResult> {
    for (;;) {
        if (options?.signal?.aborted) throw new DOMException("Aborted", "AbortError");
        let state: VideoGenerationTaskState;
        try { state = await pollVideoGenerationTask(config, task, options); } catch (error) {
            if (options?.signal?.aborted) throw error;
            await delay(5000, options?.signal);
            continue;
        }
        if (state.status === "completed") return state.result;
        if (state.status === "failed") throw videoTaskFailed(state.error);
        await delay(5000, options?.signal);
    }
}

export function isVideoTaskFailed(error: unknown) {
    return error instanceof Error && error.name === "VideoTaskFailed";
}

function videoTaskFailed(message: string) {
    const error = new Error(message);
    error.name = "VideoTaskFailed";
    return error;
}

export async function createVideoGenerationTask(config: AiConfig, prompt: string, references: ReferenceImage[] = [], options?: VideoMediaOptions): Promise<VideoGenerationTask> {
    const selectedModel = (config.videoModel || config.model).trim();
    const requestConfig = resolveModelRequestConfig(config, selectedModel);
    assertVideoConfig(requestConfig, requestConfig.model);
    return createOpenAIVideoTask(requestConfig, selectedModel, prompt, references, options);
}

export async function pollVideoGenerationTask(config: AiConfig, task: VideoGenerationTask, options?: RequestOptions): Promise<VideoGenerationTaskState> {
    const requestConfig = resolveModelRequestConfig(config, task.model);
    assertVideoConfig(requestConfig, requestConfig.model);
    return pollOpenAIVideoTask(requestConfig, task, options);
}

export async function storeGeneratedVideo(result: VideoGenerationResult): Promise<UploadedFile> {
    if (result.blob) return uploadMediaFile(result.blob, "video");
    if (result.url) {
        try {
            return await uploadMediaFile(result.url, "video");
        } catch {
            return { url: result.url, storageKey: "", bytes: 0, mimeType: result.mimeType || "video/mp4" };
        }
    }
    throw new Error(apiText("noPlayableVideo"));
}

async function createOpenAIVideoTask(config: AiConfig, model: string, prompt: string, references: ReferenceImage[], options?: VideoMediaOptions): Promise<VideoGenerationTask> {
    const seedance = /^(doubao-)?seedance-/i.test(modelOptionName(model));
    if (!seedance && options?.videos?.length) throw new Error(apiText("invalidReferenceVideo"));
    if (!seedance && options?.audios?.length) throw new Error(apiText("invalidReferenceAudio"));
    const imageUrls: string[] = [];
    for (const image of references) {
        imageUrls.push(seedance ? await uploadVideoReference(config, image, "image", options) : await imageToDataUrl(image, { signal: options?.signal }));
    }
    if (!seedance && imageUrls.some((url) => !url.startsWith("data:image/"))) throw new Error(apiText("referenceImageReadFailed"));

    // The gateway adapter translates this stable contract for the selected provider.
    const body: Record<string, unknown> = {
        model: modelOptionName(model),
        prompt,
        duration: Number(normalizeVideoSeconds(config.videoSeconds)),
        resolution: normalizeVideoResolution(config.vquality),
    };
    const aspectRatio = inferVideoRatio(config.size);
    if (aspectRatio !== "auto") body.aspect_ratio = aspectRatio;
    if (imageUrls.length === 1) body.image = { url: imageUrls[0] };
    if (imageUrls.length > 1) body.reference_images = imageUrls.map((url) => ({ url }));
    if (seedance) {
        const videos: string[] = [];
        const audios: string[] = [];
        for (const video of options?.videos || []) videos.push(await uploadVideoReference(config, video, "video", options));
        for (const audio of options?.audios || []) audios.push(await uploadVideoReference(config, audio, "audio", options));
        if (videos.length) body.videos = videos;
        if (audios.length) body.audios = audios;
    }
    try {
        const created = unwrapVideoResponse((await axios.post<ApiVideoResponse>(aiApiUrl(config, "/videos/generations"), body, { headers: aiHeaders(config, "application/json"), signal: options?.signal })).data);
        const id = videoTaskId(created);
        if (!id) throw new Error(apiText("noVideoTaskId"));
        return { id, provider: "openai", model };
    } catch (error) {
        if (axios.isCancel(error) || options?.signal?.aborted) throw error;
        throw new Error(readAxiosError(error, apiText("videoTaskCreateFailed")));
    }
}

async function uploadVideoReference(config: AiConfig, reference: { url?: string; dataUrl?: string; storageKey?: string }, kind: "image" | "video" | "audio", options?: RequestOptions): Promise<string> {
    options?.signal?.throwIfAborted();
    const stored = reference.storageKey ? await (kind === "image" ? getImageBlob(reference.storageKey) : getMediaBlob(reference.storageKey)) : null;
    let blob = stored;
    if (!blob) {
        const source = reference.dataUrl || reference.url || "";
        if (/^https:\/\//i.test(source)) return source;
        assertAllowedMediaDownloadUrl(source);
        const response = await fetch(source, { signal: options?.signal });
        if (!response.ok) throw new Error("参考素材读取失败，请重新上传。");
        blob = await response.blob();
    }
    if (!blob.size || blob.size > 32 * 1024 * 1024) throw new Error("参考素材不能为空，单个文件最大 32 MiB。");
    const form = new FormData();
    form.append("file", blob, "reference");
    try {
        const { data } = await axios.post<{ url: string }>(aiApiUrl(config, "/video-references"), form, { headers: aiHeaders(config), signal: options?.signal });
        if (!/^https:\/\//i.test(data.url || "")) throw new Error("参考素材上传后未返回 HTTPS 地址。");
        return data.url;
    } catch (error) {
        if (axios.isCancel(error) || options?.signal?.aborted) throw error;
        throw new Error(readAxiosError(error, "参考素材上传失败。"));
    }
}

async function pollOpenAIVideoTask(config: AiConfig, task: VideoGenerationTask, options?: RequestOptions): Promise<VideoGenerationTaskState> {
    try {
        const video = unwrapVideoResponse((await axios.get<ApiVideoResponse>(aiApiUrl(config, `/videos/${task.id}`), { headers: aiHeaders(config), signal: options?.signal })).data);
        const status = videoStatus(video);
        if (["failed", "cancelled", "canceled", "expired"].includes(status)) return { status: "failed", error: readApiErrorMessage(video.error) || apiText("videoGenerationFailed") };
        if (["done", "completed"].includes(status)) {
            try {
                const content = await axios.get<Blob>(aiApiUrl(config, `/videos/${task.id}/content`), { headers: aiHeaders(config), responseType: "blob", signal: options?.signal });
                await assertVideoBlob(content.data);
                return { status: "completed", result: { blob: content.data } };
            } catch (error) {
                if (axios.isCancel(error) || options?.signal?.aborted) throw error;
                const url = videoResultUrl(video);
                if (url) return { status: "completed", result: await videoResultFromUrl(url, options) };
                throw error;
            }
        }
        const url = videoResultUrl(video);
        if (url) return { status: "completed", result: await videoResultFromUrl(url, options) };
        return { status: "pending" };
    } catch (error) {
        if (axios.isCancel(error) || options?.signal?.aborted) throw error;
        throw new Error(readAxiosError(error, apiText("videoTaskQueryFailed")));
    }
}

async function videoResultFromUrl(url: string, options?: RequestOptions): Promise<VideoGenerationResult> {
    try {
        const response = await axios.get<Blob>(url, { responseType: "blob", signal: options?.signal });
        await assertVideoBlob(response.data);
        return { blob: response.data };
    } catch (error) {
        if (axios.isCancel(error) || options?.signal?.aborted) throw error;
        return { url, mimeType: "video/mp4" };
    }
}

function assertVideoConfig(config: AiConfig, model: string) {
    if (!model) throw new Error(apiText("videoModelRequired"));
    if (!config.apiKey.trim()) throw new Error(apiText("apiKeyRequired"));
}

function normalizeVideoSeconds(value: string) {
    return clampVideoSeconds(value);
}

function normalizeVideoResolution(value: string) {
    return `${parseVideoResolution(value)}p`;
}

function unwrapVideoResponse(payload: ApiVideoResponse): VideoResponse {
    const response = unwrapEnvelope(payload, apiText("noVideoTask"));
    return response.data && !hasVideoPayload(response) ? unwrapVideoResponse(response.data) : response;
}

function hasVideoPayload(value: VideoResponse) {
    return Boolean(value.id || value.request_id || value.task_id || value.status || value.url || value.result_url || value.video_url || value.video || value.content);
}

function videoTaskId(value: VideoResponse) {
    return [value, value.data || undefined, value.video || undefined, value.data?.video || undefined]
        .flatMap((item) => item ? [item.id, item.request_id, item.task_id] : [])
        .find((id): id is string => typeof id === "string" && Boolean(id));
}

function videoStatus(value: VideoResponse) {
    return (value.status || value.video?.status || value.data?.status || "").toLowerCase();
}

function unwrapEnvelope<T>(payload: ApiEnvelope<T>, emptyMessage: string): T {
    if (!payload) throw new Error(emptyMessage);
    if (typeof payload === "object" && "code" in payload && payload.code !== undefined) {
        if (payload.code !== 0 && payload.code !== "0") throw new Error(readApiErrorMessage(payload) || apiText("requestFailed"));
        if (!payload.data) throw new Error(emptyMessage);
        return payload.data;
    }
    return payload as T;
}

function videoResultUrl(payload: VideoResponse) {
    return [payload.video_url, payload.result_url, payload.url, payload.content?.video_url, payload.content?.url, payload.video?.video_url, payload.video?.result_url, payload.video?.url, payload.video?.content?.video_url, payload.video?.content?.url].find((url) => typeof url === "string" && (isPublicMediaUrl(url) || /\.mp4(\?|#|$)/i.test(url)));
}

function readApiErrorMessage(value: unknown): string {
    if (!value) return "";
    if (typeof value === "string") {
        try {
            const parsed = JSON.parse(value);
            const inner = readApiErrorMessage(parsed) || value;
            if (inner === value && typeof parsed === "object" && Object.keys(parsed).length === 0) return "";
            return inner;
        } catch {
            if (/<[a-z][\s\S]*>/i.test(value)) return apiText("htmlError", { preview: `${value.slice(0, 80)}...` });
            return value;
        }
    }
    if (typeof value !== "object") return "";
    const payload = value as { msg?: unknown; message?: unknown; error?: unknown; detail?: unknown };
    // error may be a string or an object containing a message.
    const errorMsg =
        typeof payload.error === "string"
            ? payload.error
            : (payload.error as { message?: unknown })?.message;
    return (
        readApiErrorMessage(payload.msg) ||
        readApiErrorMessage(payload.message) ||
        readApiErrorMessage(errorMsg) ||
        readApiErrorMessage(payload.detail) ||
        ""
    );
}

function readAxiosError(error: unknown, fallback: string) {
    if (axios.isCancel(error)) return apiText("requestCanceled");
    if (axios.isAxiosError<{ error?: { message?: string }; msg?: string; message?: string; code?: number | string }>(error)) {
        if (!error.response && error.code === "ERR_NETWORK") return apiText("requestFailed");
        const responseData = error.response?.data;
        return readApiErrorMessage(responseData) || statusMessage(error.response?.status, fallback);
    }
    if (error instanceof DOMException && error.name === "AbortError") return apiText("requestCanceled");
    return error instanceof Error ? readApiErrorMessage(error.message) || error.message : fallback;
}

function statusMessage(status: number | undefined, fallback: string) {
    if (status === 401 || status === 403) return apiText("authenticationFailed");
    if (status === 429) return apiText("rateLimited");
    return status ? `${fallback}（${status}）` : fallback;
}

async function assertVideoBlob(blob: Blob) {
    if (!blob.type.includes("json")) return;
    let payload: { code?: number; msg?: string; error?: { message?: string } };
    try {
        payload = JSON.parse(await blob.text()) as { code?: number; msg?: string; error?: { message?: string } };
    } catch {
        return;
    }
    if (typeof payload.code === "number" && payload.code !== 0) throw new Error(readApiErrorMessage(payload) || apiText("videoDownloadFailed"));
    if (payload.error?.message) throw new Error(readApiErrorMessage(payload.error.message) || payload.error.message);
}

function isPublicMediaUrl(value: string) {
    return /^https?:\/\//i.test(value || "");
}

function delay(ms: number, signal?: AbortSignal) {
    return new Promise<void>((resolve, reject) => {
        if (signal?.aborted) {
            reject(new DOMException("Aborted", "AbortError"));
            return;
        }
        const timer = setTimeout(resolve, ms);
        signal?.addEventListener(
            "abort",
            () => {
                clearTimeout(timer);
                reject(new DOMException("Aborted", "AbortError"));
            },
            { once: true },
        );
    });
}
