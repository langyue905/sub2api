import axios from "axios";

import i18n from "@/i18n";
import { selectVideoFrameImages } from "@/lib/video-references";
import { h3VideoOptions, isMiniMaxH3, validateH3References } from "@/lib/minimax-h3";
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
    metadata?: { url?: string };
    content?: { video_url?: string; url?: string } | null;
    video?: VideoResponse;
    data?: VideoResponse | VideoResponse[] | null;
    progress?: string | number;
};
type ApiVideoResponse = VideoResponse | { code?: number | string; data?: VideoResponse | null; msg?: string; message?: string; error?: { message?: string } };
type ApiEnvelope<T> = T | { code?: number | string; data?: T | null; msg?: string; message?: string; error?: { message?: string } };
type RequestOptions = { signal?: AbortSignal };
type VideoMediaOptions = RequestOptions & { videos?: ReferenceVideo[]; audios?: ReferenceAudio[] };
type WaitOptions = RequestOptions & {
    onQueryError?: (error: unknown) => void | Promise<void>;
    onPending?: (progress?: string | number) => void | Promise<void>;
};
const apiText = (key: string, options?: Record<string, unknown>) => i18n.t(`apiErrors.${key}`, options);

export type VideoGenerationResult = { blob?: Blob; url?: string; mimeType?: string };
export type VideoGenerationTask = { id: string; provider: "openai"; model: string; requestKey?: string };
export type VideoGenerationTaskState = { status: "pending"; progress?: string | number } | { status: "completed"; result: VideoGenerationResult } | { status: "failed"; error: string };


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

export async function waitForVideoGenerationTask(config: AiConfig, task: VideoGenerationTask, options?: WaitOptions): Promise<VideoGenerationResult> {
    let queryErrors = 0;
    for (;;) {
        if (options?.signal?.aborted) throw new DOMException("Aborted", "AbortError");
        let state: VideoGenerationTaskState;
        try { state = await pollVideoGenerationTask(config, task, options); } catch (error) {
            if (options?.signal?.aborted) throw error;
            if (error instanceof Error && error.name === "VideoTaskConfigurationChanged") throw error;
            await options?.onQueryError?.(error);
            await delay(Math.min(30000, 5000 * 2 ** Math.min(queryErrors++, 3)), options?.signal);
            continue;
        }
        queryErrors = 0;
        if (state.status === "completed") return state.result;
        if (state.status === "failed") throw videoTaskFailed(state.error);
        await options?.onPending?.(state.progress);
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
    if (task.requestKey && task.requestKey !== await videoTaskRequestKey(requestConfig)) {
        const error = new Error("请恢复创建此任务时的 API 配置，再继续查询。当前任务不会重新提交。");
        error.name = "VideoTaskConfigurationChanged";
        throw error;
    }
    return pollOpenAIVideoTask(requestConfig, task, options);
}

// Persist an identity, never a second copy of the API key, alongside task IDs.
async function videoTaskRequestKey(config: AiConfig) {
    const bytes = new TextEncoder().encode(`${aiApiUrl(config, "")}\n${config.apiKey.trim()}`);
    const digest = await crypto.subtle.digest("SHA-256", bytes);
    return Array.from(new Uint8Array(digest), (value) => value.toString(16).padStart(2, "0")).join("");
}

export async function storeGeneratedVideo(result: VideoGenerationResult): Promise<UploadedFile> {
    if (result.blob) {
        try { return await uploadMediaFile(result.blob, "video"); } catch (error) {
            if (!result.url) throw error;
            return { url: result.url, storageKey: "", bytes: result.blob.size, mimeType: result.blob.type || "video/mp4" };
        }
    }
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
    const requestKey = await videoTaskRequestKey(config);
    const h3 = isMiniMaxH3(model);
    if (h3) references = selectVideoFrameImages(references, config.videoMode, config.firstFrameId, config.lastFrameId);
    // Validate everything before uploading media or submitting a billable create.
    const h3Options = h3 ? h3VideoOptions(config.vquality, config.size, config.videoSeconds, prompt) : null;
    if (h3) validateH3References(references.length, options?.videos?.length || 0, options?.audios?.length || 0, config.vquality, config.videoMode);
    const seedance = /^(doubao-)?seedance-/i.test(modelOptionName(model));
    if (!h3 && !seedance && options?.videos?.length) throw new Error(apiText("invalidReferenceVideo"));
    if (!h3 && !seedance && options?.audios?.length) throw new Error(apiText("invalidReferenceAudio"));
    const imageUrls: string[] = [];
    for (const image of references) {
        imageUrls.push(seedance || h3 ? await uploadVideoReference(config, image, "image", options) : await imageToDataUrl(image, { signal: options?.signal }));
    }
    if (!h3 && !seedance && imageUrls.some((url) => !url.startsWith("data:image/"))) throw new Error(apiText("referenceImageReadFailed"));

    // The gateway adapter translates this stable contract for the selected provider.
    const body: Record<string, unknown> = {
        model: modelOptionName(model),
        prompt,
        duration: Number(normalizeVideoSeconds(config.videoSeconds)),
        resolution: normalizeVideoResolution(config.vquality),
    };
    const aspectRatio = inferVideoRatio(config.size);
    if (h3Options) { delete body.duration; Object.assign(body, h3Options); }
    else if (aspectRatio !== "auto") body.aspect_ratio = aspectRatio;
    if (h3) {
        if (imageUrls.length) body.images = imageUrls;
        if (config.videoMode === "first_last_frame") body.mode = "first_last_frame";
    } else {
        if (imageUrls.length === 1) body.image = { url: imageUrls[0] };
        if (imageUrls.length > 1) body.reference_images = imageUrls.map((url) => ({ url }));
    }
    if (seedance || h3) {
        if (seedance) body.generate_audio = config.videoGenerateAudio !== "false";
        const videos: string[] = [];
        const audios: string[] = [];
        for (const video of options?.videos || []) videos.push(await uploadVideoReference(config, video, "video", options));
        for (const audio of options?.audios || []) audios.push(await uploadVideoReference(config, audio, "audio", options));
        if (videos.length) body[h3 ? "reference_videos" : "videos"] = videos;
        if (audios.length) body[h3 ? "reference_audios" : "audios"] = audios;
    }
    try {
        const created = unwrapVideoResponse((await axios.post<ApiVideoResponse>(aiApiUrl(config, "/videos/generations"), body, { headers: aiHeaders(config, "application/json"), signal: options?.signal })).data);
        const id = videoTaskId(created, h3);
        if (!id) {
            const error = new Error("任务提交结果未确认：接口未返回任务 ID。请先核对任务列表，不要直接重复提交。");
            error.name = "VideoSubmissionUncertain";
            throw error;
        }
        return { id, provider: "openai", model, requestKey };
    } catch (error) {
        if (axios.isCancel(error) || options?.signal?.aborted) throw error;
        if (error instanceof Error && error.name === "VideoSubmissionUncertain") throw error;
        if (axios.isAxiosError(error) && (!error.response || error.response.status >= 500)) {
            const uncertain = new Error(`任务提交结果未确认，请先核对任务列表，不要重复提交。${readAxiosError(error, apiText("videoTaskCreateFailed"))}`);
            uncertain.name = "VideoSubmissionUncertain";
            throw uncertain;
        }
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
        if (["done", "completed", "succeeded", "success"].includes(status) || (!status && videoResultUrl(video))) {
            try {
                const content = await axios.get<Blob>(aiApiUrl(config, `/videos/${task.id}/content`), { headers: aiHeaders(config), responseType: "blob", signal: options?.signal });
                const result = await videoResultFromContent(content.data, options);
                return { status: "completed", result: { ...result, url: result.url || videoResultUrl(video) } };
            } catch (error) {
                if (axios.isCancel(error) || options?.signal?.aborted) throw error;
                const url = videoResultUrl(video);
                if (url) return { status: "completed", result: await videoResultFromUrl(url, options) };
                throw error;
            }
        }
        // A URL or 99% may be present while the provider is still hosting media.
        return { status: "pending", progress: video.progress };
    } catch (error) {
        if (axios.isCancel(error) || options?.signal?.aborted) throw error;
        throw new Error(readAxiosError(error, apiText("videoTaskQueryFailed")));
    }
}

async function videoResultFromUrl(url: string, options?: RequestOptions): Promise<VideoGenerationResult> {
    try {
        const response = await axios.get<Blob>(url, { responseType: "blob", signal: options?.signal });
        await assertVideoBlob(response.data);
        return { blob: response.data, url };
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
    const response = unwrapEnvelope<VideoResponse>(payload, apiText("noVideoTask"));
    return response.data && !Array.isArray(response.data) && !hasVideoPayload(response) ? unwrapVideoResponse(response.data) : response;
}

function hasVideoPayload(value: VideoResponse) {
    return Boolean(value.id || value.request_id || value.task_id || value.status || value.url || value.result_url || value.video_url || value.video || value.content);
}

function videoTaskId(value: VideoResponse, h3 = false) {
    const data = Array.isArray(value.data) ? undefined : value.data;
    const items = [value, data || undefined, value.video || undefined, data?.video || undefined];
    return (h3 ? [...items.map((item) => item?.task_id), ...items.map((item) => item?.id)] : items.flatMap((item) => item ? [item.id, item.request_id, item.task_id] : []))
        .find((id): id is string => typeof id === "string" && Boolean(id));
}

function videoStatus(value: VideoResponse) {
    return (value.status || value.video?.status || (!Array.isArray(value.data) && value.data?.status) || "").toLowerCase();
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
    const data = Array.isArray(payload.data) ? payload.data : payload.data ? [payload.data] : [];
    return [payload.result_url, payload.video_url, payload.url, payload.metadata?.url, payload.content?.video_url, payload.content?.url, payload.video?.video_url, payload.video?.result_url, payload.video?.url, payload.video?.content?.video_url, payload.video?.content?.url, ...data.flatMap((item) => [item.result_url, item.video_url, item.url, item.metadata?.url])].find((url): url is string => typeof url === "string" && isPublicMediaUrl(url));
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
    const payload = value as { msg?: unknown; message?: unknown; error?: unknown; detail?: unknown; code?: unknown };
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
        (typeof payload.code === "string" && payload.code.startsWith("media_storage_") ? payload.code : "") ||
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
    if (!blob.size || blob.type.includes("json") || blob.type.startsWith("text/") || blob.type.includes("html")) {
        throw new Error(apiText("videoDownloadFailed"));
    }
}

async function videoResultFromContent(blob: Blob, options?: RequestOptions): Promise<VideoGenerationResult> {
    if (blob.type.includes("json")) {
        const payload = JSON.parse(await blob.text()) as ApiVideoResponse;
        const message = readApiErrorMessage(payload);
        if (message) throw new Error(message);
        const url = videoResultUrl(unwrapVideoResponse(payload));
        if (!url) throw new Error(apiText("noPlayableVideo"));
        return videoResultFromUrl(url, options);
    }
    await assertVideoBlob(blob);
    return { blob };
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
        const onAbort = () => {
            clearTimeout(timer);
            reject(new DOMException("Aborted", "AbortError"));
        };
        const timer = setTimeout(() => {
            signal?.removeEventListener("abort", onAbort);
            resolve();
        }, ms);
        signal?.addEventListener("abort", onAbort, { once: true });
    });
}
