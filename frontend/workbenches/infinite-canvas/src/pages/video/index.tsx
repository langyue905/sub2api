import { ArrowLeft, ArrowRight, BookOpen, CheckSquare, CircleStop, ClipboardPaste, Download, FolderPlus, History, LoaderCircle, Plus, SlidersHorizontal, Sparkles, Trash2, Upload, VideoIcon } from "lucide-react";
import { useEffect, useRef, useState, useSyncExternalStore, type DragEvent } from "react";
import { App, Button, Checkbox, Drawer, Empty, Input, Modal, Tag, Typography } from "antd";
import localforage from "localforage";
import { nanoid } from "nanoid";
import { saveAs } from "file-saver";
import { useTranslation } from "react-i18next";

import { AssetPickerModal, type InsertAssetPayload } from "@/components/canvas/asset-picker-modal";
import { ModelPicker } from "@/components/model-picker";
import { PromptSelectDialog } from "@/components/prompts/prompt-select-dialog";
import { isMiniMaxH3, validateH3References } from "@/lib/minimax-h3";
import { normalizeVideoMode, selectVideoFrameImages } from "@/lib/video-references";
import { VideoFrameSelector } from "@/components/video-frame-selector";
import { VideoMediaReferences } from "@/components/video-media-references";
import { restoreReferenceMedia, serializeReferenceMedia } from "@/services/video-reference-history";
import type { ReferenceAudio, ReferenceVideo } from "@/types/media";
import { VideoSettingsPanel, videoResolutionLabel, normalizeVideoResolutionValue, normalizeVideoSizeValue, videoSizeLabel } from "@/components/video-settings-panel";
import { canvasThemes } from "@/lib/canvas-theme";
import { clampVideoSeconds } from "@/lib/media-size";
import { formatBytes, formatDuration } from "@/lib/image-utils";
import { deleteStoredMedia, resolveMediaUrl } from "@/services/file-storage";
import { resolveImageUrl, ensureImagePreview, getImagePreviewRevision, previewUrlFor, subscribeImagePreviews, uploadImage } from "@/services/image-storage";
import { createVideoGenerationTask, isVideoTaskFailed, waitForVideoGenerationTask, storeGeneratedVideo, type VideoGenerationTask } from "@/services/api/video";
import { canContinueVideoTask, shouldResumeVideoTask } from "@/services/video-task-history";
import { useAssetStore } from "@/stores/use-asset-store";
import { boolConfig, modelOptionLabel, useConfigStore, useEffectiveConfig, type AiConfig } from "@/stores/use-config-store";
import { useThemeStore } from "@/stores/use-theme-store";
import type { ReferenceImage } from "@/types/image";
import i18n from "@/i18n";

type GeneratedVideo = {
    id: string;
    url: string;
    storageKey: string;
    durationMs: number;
    width: number;
    height: number;
    bytes: number;
    mimeType: string;
};

type GenerationResult = {
    id: string;
    status: "pending" | "success" | "failed" | "stopped" | "interrupted";
    video?: GeneratedVideo;
    error?: string;
    progress?: string | number;
};

type GenerationLog = {
    id: string;
    createdAt: number;
    title: string;
    prompt: string;
    time: string;
    model: string;
    config: GenerationLogConfig;
    references: ReferenceImage[];
    referenceVideos: ReferenceVideo[];
    referenceAudios: ReferenceAudio[];
    durationMs: number;
    size: string;
    resolution: string;
    seconds: string;
    status: GenerationResult["status"];
    task?: VideoGenerationTask;
    video?: GeneratedVideo;
    error?: string;
    failureSource?: "upstream";
};

type GenerationLogConfig = Pick<AiConfig, "model" | "videoModel" | "size" | "vquality" | "videoSeconds" | "videoGenerateAudio" | "videoWatermark" | "videoMode" | "firstFrameId" | "lastFrameId">;

type UpdateAiConfig = <K extends keyof AiConfig>(key: K, value: AiConfig[K]) => void;

const LOG_STORE_KEY = "infinite-canvas:video_generation_logs";
const logStore = localforage.createInstance({ name: "infinite-canvas", storeName: "video_generation_logs" });

export default function VideoPage() {
    const { message, modal } = App.useApp();
    const { t } = useTranslation();
    useSyncExternalStore(subscribeImagePreviews, getImagePreviewRevision);
    const fileInputRef = useRef<HTMLInputElement>(null);
    const dragDepthRef = useRef(0);
    const activeLogIdsRef = useRef<Set<string>>(new Set());
    const pollControllersRef = useRef<Map<string, AbortController>>(new Map());
    const creationControllerRef = useRef<AbortController | null>(null);
    const mountedRef = useRef(true);
    const config = useConfigStore((state) => state.config);
    const effectiveConfig = useEffectiveConfig();
    const updateConfig = useConfigStore((state) => state.updateConfig);
    const isAiConfigReady = useConfigStore((state) => state.isAiConfigReady);
    const openConfigDialog = useConfigStore((state) => state.openConfigDialog);
    const addAsset = useAssetStore((state) => state.addAsset);
    const [prompt, setPrompt] = useState("");
    const [references, setReferences] = useState<ReferenceImage[]>([]);
    const [referenceVideos, setReferenceVideos] = useState<ReferenceVideo[]>([]);
    const [referenceAudios, setReferenceAudios] = useState<ReferenceAudio[]>([]);
    const [frameSelection, setFrameSelection] = useState<{ firstFrameId?: string; lastFrameId?: string }>({});
    const [mediaBusy, setMediaBusy] = useState({ video: false, audio: false, image: false });
    const [results, setResults] = useState<GenerationResult[]>([]);
    const [logs, setLogs] = useState<GenerationLog[]>([]);
    const [running, setRunning] = useState(false);
    const [logsOpen, setLogsOpen] = useState(false);
    const [settingsOpen, setSettingsOpen] = useState(false);
    const [promptDialogOpen, setPromptDialogOpen] = useState(false);
    const [assetPickerOpen, setAssetPickerOpen] = useState(false);
    const [startedAt, setStartedAt] = useState(0);
    const [elapsedMs, setElapsedMs] = useState(0);
    const [selectedLogIds, setSelectedLogIds] = useState<string[]>([]);
    const [previewLog, setPreviewLog] = useState<GenerationLog | null>(null);
    const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
    const [referenceDragTarget, setReferenceDragTarget] = useState(false);

    const model = effectiveConfig.videoModel || effectiveConfig.model;
    const maxReferenceImages = isMiniMaxH3(model) ? 9 : 7;
    const firstLastFrame = isMiniMaxH3(model) && effectiveConfig.videoMode === "first_last_frame";
    const supportsMedia = isMiniMaxH3(model) || /^(doubao-)?seedance-/i.test(modelOptionLabel(effectiveConfig, model));
    const canGenerate = Boolean(prompt.trim()) && !Object.values(mediaBusy).some(Boolean);
    const uploading = Object.values(mediaBusy).some(Boolean);

    useEffect(() => {
        if (!running || !startedAt) return;
        const timer = window.setInterval(() => setElapsedMs(performance.now() - startedAt), 1000);
        return () => window.clearInterval(timer);
    }, [running, startedAt]);

    useEffect(() => {
        void refreshLogs();
    }, []);

    useEffect(() => {
        mountedRef.current = true;
        return () => {
            mountedRef.current = false;
            creationControllerRef.current?.abort();
            pollControllersRef.current.forEach((controller) => controller.abort());
            pollControllersRef.current.clear();
        };
    }, []);

    const addReferences = async (files?: FileList | null) => {
        if (uploading) return;
        setMediaBusy((value) => ({ ...value, image: true }));
        try {
        const selectedFiles = Array.from(files || []);
        const unsupported = selectedFiles.filter((file) => !file.type.startsWith("image/"));
        if (unsupported.length) message.warning(t("videoWorkbench.unsupportedFiles"));
        const imageFiles = selectedFiles.filter((file) => file.type.startsWith("image/")).slice(0, Math.max(0, maxReferenceImages - references.length));
        const nextReferences = await Promise.all(
            imageFiles.map(async (file) => {
                if (!file.size || file.size > 32 * 1024 * 1024) throw new Error("单个素材不能为空，最大32 MiB");
                const image = await uploadImage(file);
                return { id: nanoid(), name: file.name, type: image.mimeType, dataUrl: image.url, storageKey: image.storageKey };
            }),
        );
        setReferences((value) => [...value, ...nextReferences].slice(0, maxReferenceImages));
        } catch (error) { message.error(error instanceof Error ? error.message : String(error)); }
        finally { setMediaBusy((value) => ({ ...value, image: false })); }
    };

    const handleReferenceDragEnter = (event: DragEvent<HTMLDivElement>) => {
        event.preventDefault();
        dragDepthRef.current += 1;
        if (event.dataTransfer.types.includes("Files")) setReferenceDragTarget(true);
    };

    const handleReferenceDragLeave = (event: DragEvent<HTMLDivElement>) => {
        event.preventDefault();
        dragDepthRef.current = Math.max(0, dragDepthRef.current - 1);
        if (!dragDepthRef.current) setReferenceDragTarget(false);
    };

    const handleReferenceDrop = (event: DragEvent<HTMLDivElement>) => {
        event.preventDefault();
        dragDepthRef.current = 0;
        setReferenceDragTarget(false);
        void addReferences(event.dataTransfer.files);
    };

    const addReferencesFromClipboard = async () => {
        if (uploading) return;
        setMediaBusy((value) => ({ ...value, image: true }));
        try {
            const items = await navigator.clipboard.read();
            const blobs = await Promise.all(items.flatMap((item) => item.types.filter((type) => type.startsWith("image/")).map((type) => item.getType(type))));
            if (!blobs.length) {
                message.error(t("videoWorkbench.clipboardEmpty"));
                return;
            }
            const nextReferences = await Promise.all(
                blobs.slice(0, Math.max(0, maxReferenceImages - references.length)).map(async (blob, index) => {
                    if (!blob.size || blob.size > 32 * 1024 * 1024) throw new Error("单个素材不能为空，最大32 MiB");
                    const image = await uploadImage(blob);
                    return { id: nanoid(), name: `clipboard-${index + 1}.png`, type: image.mimeType, dataUrl: image.url, storageKey: image.storageKey };
                }),
            );
            setReferences((value) => [...value, ...nextReferences].slice(0, maxReferenceImages));
            message.success(t("videoWorkbench.clipboardAdded", { count: nextReferences.length }));
        } catch (error) { message.error(error instanceof Error ? error.message : t("videoWorkbench.clipboardEmpty")); }
        finally { setMediaBusy((value) => ({ ...value, image: false })); }
    };
    const generate = async () => {
        const snapshot = buildRequestSnapshot();
        if (!snapshot) {
            return;
        }
        setElapsedMs(0);
        setRunning(true);
        setPreviewLog(null);
        setResults([{ id: nanoid(), status: "pending" }]);
        const batchStartedAt = performance.now();
        setStartedAt(batchStartedAt);
        const controller = new AbortController();
        creationControllerRef.current = controller;
        let handedToPolling = false;
        let log = { ...buildLog({ prompt: snapshot.text, model, config: snapshot.config, references: snapshot.references, durationMs: 0, status: "pending" }), referenceVideos: snapshot.videos, referenceAudios: snapshot.audios };
        try {
            // Persist before POST. Reloading an uncertain submission must never POST again.
            await saveLog(log, false);
            const task = await createVideoGenerationTask(snapshot.config, snapshot.text, snapshot.references, { signal: controller.signal, videos: snapshot.videos, audios: snapshot.audios });
            log = { ...log, task };
            await saveLog(log, false);
            throwIfAborted(controller.signal);
            handedToPolling = true;
            void pollGenerationLog(log, snapshot.config, controller);
        } catch (error) {
            if (controller.signal.aborted) {
                if (!mountedRef.current) return;
                const errorMessage = localStopMessage();
                setResults([{ id: log.id, status: "stopped", error: errorMessage }]);
                await saveLog({ ...log, durationMs: performance.now() - batchStartedAt, status: "stopped", error: errorMessage }, false, true);
                setRunning(false);
                setStartedAt(0);
                return;
            }
            const errorMessage = error instanceof Error ? error.message : t("workbench.generationFailed");
            const status = log.task || (error instanceof Error && error.name === "VideoSubmissionUncertain") ? "interrupted" : "failed";
            setResults([{ id: log.id, status, error: errorMessage }]);
            await saveLog({ ...log, durationMs: performance.now() - batchStartedAt, status, error: errorMessage }, false, true);
            message.error(errorMessage);
            setRunning(false);
            setStartedAt(0);
        } finally {
            if (!handedToPolling && creationControllerRef.current === controller) creationControllerRef.current = null;
        }
    };

    const buildRequestSnapshot = () => {
        const text = prompt.trim();
        if (!text) {
            message.error(t("videoWorkbench.promptRequired"));
            return null;
        }
        if (!isAiConfigReady(effectiveConfig, model)) {
            message.warning(t("workbench.configFirst"));
            openConfigDialog(true);
            return null;
        }
        const requestConfig = { ...buildVideoConfig(effectiveConfig, model), ...frameSelection };
        try {
            if (isMiniMaxH3(model)) {
                const images = selectVideoFrameImages(references, requestConfig.videoMode, frameSelection.firstFrameId, frameSelection.lastFrameId);
                validateH3References(images.length, referenceVideos.length, referenceAudios.length, requestConfig.vquality, requestConfig.videoMode);
            }
            if (!supportsMedia && (referenceVideos.length || referenceAudios.length)) throw new Error(videoText("当前模型不支持音视频参考，请移除素材或更换模型。", "Remove media references or select a model that supports them."));
        } catch (error) {
            message.error(error instanceof Error ? error.message : String(error)); return null;
        }
        return { text, config: requestConfig, references: [...references], videos: [...referenceVideos], audios: [...referenceAudios] };
    };

    const retryResult = () => {
        modal.confirm({
            title: videoText("创建新视频任务？", "Create a new video task?"),
            content: videoText("重新生成会创建新任务，可能再次计费。若原任务仍在处理，请使用“继续查询”。", "A new task may incur another charge. Use Continue querying if the original task is still processing."),
            okText: videoText("重新生成", "Generate again"),
            cancelText: t("common.cancel"),
            onOk: () => { void generate(); },
        });
    };

    const stopWaiting = () => {
        creationControllerRef.current?.abort();
        pollControllersRef.current.forEach((controller) => controller.abort());
    };

    const downloadVideo = (video: GeneratedVideo) => {
        saveAs(video.url, "video.mp4");
    };

    const saveResultToAssets = (video: GeneratedVideo) => {
        addAsset({
            kind: "video",
            title: t("videoWorkbench.resultTitle"),
            coverUrl: "",
            tags: [],
            source: t("videoWorkbench.source"),
            data: { url: video.url, storageKey: video.storageKey, width: video.width, height: video.height, bytes: video.bytes, mimeType: video.mimeType },
            metadata: { source: "video-page", prompt },
        });
        message.success(t("common.addedToAssets"));
    };

    const insertPickedAsset = async (payload: InsertAssetPayload) => {
        if (uploading) return;
        if (payload.kind === "text") {
            setPrompt(payload.content);
        } else if (payload.kind === "image") {
            const stored = await uploadImage(payload.dataUrl);
            setReferences((value) => [...value, { id: nanoid(), name: payload.title, type: stored.mimeType, dataUrl: stored.url, storageKey: stored.storageKey }].slice(0, maxReferenceImages));
        } else if (payload.kind === "video") {
            if (referenceVideos.length >= 3) { message.warning(videoText("参考视频最多3个", "Maximum 3 reference videos")); return; }
            setReferenceVideos((items) => [...items, { id: nanoid(), name: payload.title, type: "video/mp4", url: payload.url, storageKey: payload.storageKey, width: payload.width, height: payload.height }].slice(0, 3));
        }
        setAssetPickerOpen(false);
    };

    const createSession = () => {
        if (uploading) { message.info(videoText("素材上传中，请稍候。", "Wait for the reference upload to finish.")); return; }
        setPrompt("");
        setReferences([]);
        setReferenceVideos([]);
        setReferenceAudios([]);
        setFrameSelection({});
        setResults([]);
        setElapsedMs(0);
        setStartedAt(0);
        setSelectedLogIds([]);
        setPreviewLog(null);
    };

    const deleteSelectedLogs = () => {
        const mediaKeys = logs
            .filter((log) => selectedLogIds.includes(log.id))
            .map((log) => log.video?.storageKey)
            .filter((key): key is string => Boolean(key));
        void Promise.all([deleteStoredMedia(mediaKeys), ...selectedLogIds.map((id) => logStore.removeItem(id))]).then(() => refreshLogs());
        if (previewLog && selectedLogIds.includes(previewLog.id)) {
            setPreviewLog(null);
            setResults([]);
        }
        setSelectedLogIds([]);
        setDeleteConfirmOpen(false);
    };

    const saveLog = async (log: GenerationLog, resumePending = true, tolerateStorageFailure = false) => {
        try {
            await logStore.setItem(log.id, serializeLog(log));
            await refreshLogs(resumePending);
        } catch (error) {
            // Keep the task available for querying even if browser storage is full.
            setLogs((items) => [log, ...items.filter((item) => item.id !== log.id)]);
            if (!tolerateStorageFailure) throw error;
        }
    };

    const refreshLogs = async (resumePending = true) => {
        const nextLogs = await readStoredLogs();
        setLogs(nextLogs);
        if (resumePending) resumePendingLogs(nextLogs);
        return nextLogs;
    };

    const resumePendingLogs = (items: GenerationLog[]) => {
        for (const log of items) {
            if (shouldResumeVideoTask(log)) void pollGenerationLog(log);
        }
    };

    const pollGenerationLog = async (log: GenerationLog, configOverride?: AiConfig, controller = new AbortController()) => {
        if (!log.task || activeLogIdsRef.current.has(log.id)) return;
        activeLogIdsRef.current.add(log.id);
        pollControllersRef.current.set(log.id, controller);
        setRunning(true);
        setStartedAt((value) => value || performance.now());
        setResults([{ id: log.id, status: "pending" }]);
        const taskConfig = buildVideoConfig({ ...effectiveConfig, ...log.config }, log.task.model || log.model);
        try {
            let queryError = "";
            await saveLog({ ...log, status: "pending", error: undefined }, false);
            const result = await waitForVideoGenerationTask(configOverride || taskConfig, log.task, {
                signal: controller.signal,
                onQueryError: async (error) => {
                    const text = `${videoText("查询暂时中断，正在重连；原任务仍保留。", "Query interrupted; reconnecting to the original task.")} ${error instanceof Error ? error.message : ""}`;
                    setResults([{ id: log.id, status: "pending", error: text }]);
                    if (queryError !== text) await saveLog({ ...log, status: "pending", error: text }, false);
                    queryError = text;
                },
                onPending: async (progress) => {
                    setResults([{ id: log.id, status: "pending", progress }]);
                    if (queryError) await saveLog({ ...log, status: "pending", error: undefined }, false);
                    queryError = "";
                },
            });
            throwIfAborted(controller.signal);
            const stored = await storeGeneratedVideo(result);
            throwIfAborted(controller.signal);
            const nextVideo: GeneratedVideo = {
                id: nanoid(), url: stored.url, storageKey: stored.storageKey,
                durationMs: Date.now() - log.createdAt,
                width: stored.width || 1280, height: stored.height || 720,
                bytes: stored.bytes, mimeType: stored.mimeType,
            };
            setResults([{ id: log.id, status: "success", video: nextVideo }]);
            await saveLog({ ...log, status: "success", durationMs: nextVideo.durationMs, video: nextVideo, error: undefined }, false);
            message.success(t("videoWorkbench.generated"));
        } catch (error) {
            if (controller.signal.aborted) {
                if (!mountedRef.current) return;
                const errorMessage = localStopMessage();
                setResults([{ id: log.id, status: "stopped", error: errorMessage }]);
                await saveLog({ ...log, status: "stopped", durationMs: Date.now() - log.createdAt, error: errorMessage }, false, true);
                return;
            }
            const errorMessage = error instanceof Error ? error.message : t("workbench.generationFailed");
            const status = isVideoTaskFailed(error) ? "failed" : "interrupted";
            setResults([{ id: log.id, status, error: errorMessage }]);
            await saveLog({ ...log, status, failureSource: status === "failed" ? "upstream" : undefined, durationMs: Date.now() - log.createdAt, error: errorMessage }, false, true);
            message.error(errorMessage);
        } finally {
            activeLogIdsRef.current.delete(log.id);
            if (pollControllersRef.current.get(log.id) === controller) pollControllersRef.current.delete(log.id);
            if (creationControllerRef.current === controller) creationControllerRef.current = null;
            if (mountedRef.current && !activeLogIdsRef.current.size) {
                setRunning(false);
                setStartedAt(0);
            }
        }
    };

    const previewGenerationLog = (log: GenerationLog) => {
        if (uploading) { message.info(videoText("素材上传中，请稍候。", "Wait for the reference upload to finish.")); return; }
        setPreviewLog(log);
        setLogsOpen(false);
        setPrompt(log.prompt);
        setReferences(log.references || []);
        setReferenceVideos(log.referenceVideos || []);
        setReferenceAudios(log.referenceAudios || []);
        setFrameSelection({ firstFrameId: log.config.firstFrameId, lastFrameId: log.config.lastFrameId });
        if (log.config.videoModel || log.model) updateConfig("videoModel", log.config.videoModel || log.model);
        if (log.config.size) updateConfig("size", log.config.size);
        if (log.config.vquality) updateConfig("vquality", log.config.vquality);
        if (log.config.videoSeconds) updateConfig("videoSeconds", log.config.videoSeconds);
        if (log.config.videoGenerateAudio) updateConfig("videoGenerateAudio", log.config.videoGenerateAudio);
        if (log.config.videoWatermark) updateConfig("videoWatermark", log.config.videoWatermark);
        if (log.config.videoMode) updateConfig("videoMode", log.config.videoMode);
        const status = activeLogIdsRef.current.has(log.id) ? "pending" : canContinueVideoTask(log) ? "stopped" : log.status;
        setResults([{ id: log.id, status, video: log.video, error: log.error }]);
    };

    return (
        <div className="flex h-full flex-col overflow-hidden bg-stone-50 text-stone-900 dark:bg-stone-950 dark:text-stone-100">
            <main className="grid min-h-0 flex-1 grid-cols-1 gap-3 overflow-y-auto p-3 lg:grid-cols-[300px_minmax(0,1fr)] lg:overflow-hidden xl:grid-cols-[320px_minmax(0,1fr)]">
                <aside className="thin-scrollbar hidden min-h-0 overflow-y-auto rounded-lg border border-stone-200 bg-card p-4 shadow-sm dark:border-stone-800 lg:block">
                    <LogPanel logs={logs} selectedLogIds={selectedLogIds} activeLogId={previewLog?.id} onSelectedLogIdsChange={setSelectedLogIds} onCreateSession={createSession} onDeleteSelected={() => setDeleteConfirmOpen(true)} onPreviewLog={previewGenerationLog} />
                </aside>

                <section className="grid gap-3 lg:min-h-0 lg:overflow-hidden xl:grid-cols-[420px_minmax(0,1fr)]">
                    <div className="thin-scrollbar flex flex-col rounded-lg border border-stone-200 bg-card p-4 shadow-sm dark:border-stone-800 lg:min-h-0 lg:overflow-y-auto">
                        <div className="flex items-start justify-between gap-3">
                            <h1 className="text-2xl font-semibold text-stone-950 dark:text-stone-100">{t("videoWorkbench.title")}</h1>
                            <div className="flex shrink-0 gap-2 lg:hidden">
                                <Button icon={<History className="size-4" />} onClick={() => setLogsOpen(true)}>
                                    {t("workbench.logs")}
                                </Button>
                                <Button icon={<SlidersHorizontal className="size-4" />} onClick={() => setSettingsOpen(true)}>
                                    {t("workbench.settings")}
                                </Button>
                            </div>
                        </div>

                        <div className="mt-6 space-y-5">
                            <div>
                                <div className="mb-2 flex items-center justify-between gap-3">
                                    <span className="text-base font-semibold">{t("workbench.prompt")}</span>
                                    <div className="flex gap-2">
                                        <Button size="small" icon={<BookOpen className="size-3.5" />} onClick={() => setPromptDialogOpen(true)}>
                                            {t("workbench.viewPrompts")}
                                        </Button>
                                        <Button size="small" icon={<FolderPlus className="size-3.5" />} onClick={() => setAssetPickerOpen(true)}>
                                            {t("workbench.viewAssets")}
                                        </Button>
                                    </div>
                                </div>
                                <Input.TextArea value={prompt} onChange={(event) => setPrompt(event.target.value)} rows={7} placeholder={t("videoWorkbench.promptPlaceholder")} />
                            </div>

                            <div className="min-w-0">
                                <div className="mb-2 flex items-center justify-between gap-3">
                                    <span className="text-base font-semibold">{t("videoWorkbench.references")}</span>
                                    <div className="flex gap-2">
                                        <Button size="small" icon={<ClipboardPaste className="size-3.5" />} onClick={() => void addReferencesFromClipboard()}>
                                            {t("workbench.clipboard")}
                                        </Button>
                                        <Button size="small" icon={<Upload className="size-3.5" />} onClick={() => fileInputRef.current?.click()}>
                                            {t("workbench.upload")}
                                        </Button>
                                    </div>
                                </div>
                                <div
                                    className={`hover-scrollbar hover-scrollbar-hint flex min-h-24 w-full min-w-0 max-w-full gap-2 overflow-x-scroll overflow-y-hidden rounded-lg border border-dashed p-2 pb-3 overscroll-x-contain transition-colors ${referenceDragTarget ? "border-stone-900 bg-stone-100/80 dark:border-stone-100 dark:bg-stone-900/80" : "border-stone-300 dark:border-stone-700"}`}
                                    onDragEnter={handleReferenceDragEnter}
                                    onDragOver={(event) => {
                                        event.preventDefault();
                                        event.dataTransfer.dropEffect = "copy";
                                    }}
                                    onDragLeave={handleReferenceDragLeave}
                                    onDrop={handleReferenceDrop}
                                >
                                    {references.map((item, index) => (
                                        <div key={item.id} className="group relative size-20 shrink-0 overflow-hidden rounded-md border border-stone-200 dark:border-stone-800">
                                            <img src={previewUrlFor(item.storageKey) || item.dataUrl} alt={item.name} className="size-full object-cover" />
                                            <span className="absolute left-1 top-1 rounded bg-black/60 px-1.5 py-0.5 text-[10px] font-medium text-white">{index + 1}</span>
                                            <ReferenceOrderButtons index={index} total={references.length} onMove={(offset) => setReferences((value) => moveListItem(value, index, offset))} />
                                            <button type="button" className="absolute right-1 top-1 flex size-6 items-center justify-center rounded bg-black/60 text-white" onClick={() => setReferences((value) => value.filter((ref) => ref.id !== item.id))} aria-label={t("videoWorkbench.removeImage")}>
                                                <Trash2 className="size-3.5" />
                                            </button>
                                        </div>
                                    ))}
                                    {!references.length ? <div className="flex min-w-full items-center justify-center text-sm text-stone-500">{referenceDragTarget ? t("videoWorkbench.dropReferences") : t("videoWorkbench.noImages", { count: maxReferenceImages })}</div> : null}
                                </div>
                            </div>

                            {firstLastFrame ? <VideoFrameSelector disabled={uploading} images={references} {...frameSelection} onChange={(patch) => setFrameSelection((value) => ({ ...value, ...patch }))} onUpload={async (role, file) => {
                                if (uploading) return;
                                setMediaBusy((value) => ({ ...value, image: true }));
                                try {
                                    if (references.length >= maxReferenceImages) throw new Error(videoText("请先移除一张参考图再上传。", "Remove an image before uploading another."));
                                    if (!file.size || file.size > 32 * 1024 * 1024) throw new Error("单个素材不能为空，最大32 MiB");
                                    const stored = await uploadImage(file);
                                    const image = { id: nanoid(), name: file.name, type: stored.mimeType, dataUrl: stored.url, storageKey: stored.storageKey };
                                    setReferences((items) => [...items, image].slice(0, maxReferenceImages));
                                    setFrameSelection((value) => ({ ...value, [role]: image.id }));
                                } catch (error) { message.error(error instanceof Error ? error.message : String(error)); }
                                finally { setMediaBusy((value) => ({ ...value, image: false })); }
                            }} /> : null}
                            {supportsMedia || referenceVideos.length || referenceAudios.length ? <div className="space-y-2">
                                <p className="text-xs text-stone-500">{isMiniMaxH3(model) ? videoText("MiniMax-H3 支持9图、3音频、3参考视频；音视频参考需至少一张图片。单个素材最大32 MiB。", "MiniMax-H3: 9 images, 3 audio, 3 videos; media references require an image. Maximum 32 MiB per file.") : videoText("支持参考音视频，具体数量以上游模型能力为准。单个素材最大32 MiB。", "Media limits depend on the selected model. Maximum 32 MiB per file.")}</p>
                                <VideoMediaReferences kind="video" items={referenceVideos} onChange={setReferenceVideos} onBusyChange={(busy) => setMediaBusy((value) => ({ ...value, video: busy }))} />
                                <VideoMediaReferences kind="audio" items={referenceAudios} onChange={setReferenceAudios} onBusyChange={(busy) => setMediaBusy((value) => ({ ...value, audio: busy }))} />
                            </div> : null}

                            <div className="flex items-center justify-between rounded-lg border border-stone-200 bg-stone-50 px-3 py-2 text-sm dark:border-stone-800 dark:bg-stone-900 sm:hidden">
                                <span className="truncate text-stone-500 dark:text-stone-400">
                                    {modelOptionLabel(effectiveConfig, model)} · {videoResolutionLabel(effectiveConfig.vquality)} · {videoSizeLabel(effectiveConfig.size)} · {normalizeVideoSeconds(effectiveConfig.videoSeconds)}s
                                </span>
                                <Button size="small" type="text" icon={<SlidersHorizontal className="size-4" />} onClick={() => setSettingsOpen(true)}>
                                    {t("workbench.adjust")}
                                </Button>
                            </div>

                            <div className="hidden gap-4 sm:grid sm:grid-cols-2">
                                <GenerationSettings config={effectiveConfig} model={model} updateConfig={updateConfig} openConfigDialog={openConfigDialog} />
                            </div>
                        </div>

                        <div className="mt-auto pt-6">
                            <Button type="primary" size="large" block icon={<Sparkles className="size-4" />} loading={running} disabled={!canGenerate || running} onClick={() => void generate()}>
                                {t("workbench.generate")}
                            </Button>
                            {running ? (
                                <Button size="large" block danger className="mt-2" icon={<CircleStop className="size-4" />} title={localStopMessage()} onClick={stopWaiting}>
                                    {localStopLabel()}
                                </Button>
                            ) : null}
                        </div>
                    </div>

                    <div className="thin-scrollbar rounded-lg border border-stone-200 bg-card p-4 shadow-sm dark:border-stone-800 lg:min-h-0 lg:overflow-y-auto lg:p-5">
                        <div className="mb-4 flex items-center justify-between gap-3">
                            <h2 className="text-xl font-semibold">{t("workbench.results")}</h2>
                            {running ? <Tag className="m-0 px-2 py-1">{t("workbench.waiting", { time: formatDuration(elapsedMs) })}</Tag> : null}
                        </div>
                        {results.length ? (
                            <div className="grid gap-4">
                                {results.map((result) => {
                                    const log = logs.find((item) => item.id === result.id);
                                    const onContinue = log && canContinueVideoTask(log) ? () => { void pollGenerationLog(log); } : undefined;
                                    return result.status === "success" && result.video ? <ResultVideoCard key={result.id} video={result.video} onDownload={downloadVideo} onSaveAsset={saveResultToAssets} /> : result.status === "failed" ? <FailedVideoCard key={result.id} error={result.error || t("workbench.generationFailed")} onRetry={retryResult} /> : result.status === "stopped" || result.status === "interrupted" ? <StoppedVideoCard key={result.id} error={result.error || localStopMessage()} onRetry={retryResult} onContinue={onContinue} /> : <PendingVideoCard key={result.id} error={result.error} progress={result.progress} />;
                                })}
                            </div>
                        ) : (
                            <div className="flex min-h-[320px] flex-col items-center justify-center rounded-lg border border-dashed border-stone-300 text-center dark:border-stone-700 lg:min-h-[560px]">
                                <VideoIcon className="mb-4 size-11 text-stone-400" />
                                <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("videoWorkbench.empty")} />
                            </div>
                        )}
                    </div>
                </section>
            </main>
            <input
                ref={fileInputRef}
                type="file"
                accept="image/*"
                multiple
                className="hidden"
                onChange={(event) => {
                    void addReferences(event.target.files);
                    event.target.value = "";
                }}
            />
            <Drawer title={t("workbench.logs")} placement="bottom" size="large" open={logsOpen} onClose={() => setLogsOpen(false)}>
                <LogPanel logs={logs} selectedLogIds={selectedLogIds} activeLogId={previewLog?.id} onSelectedLogIdsChange={setSelectedLogIds} onCreateSession={createSession} onDeleteSelected={() => setDeleteConfirmOpen(true)} onPreviewLog={previewGenerationLog} />
            </Drawer>
            <Drawer title={t("workbench.settings")} placement="bottom" height="82vh" open={settingsOpen} onClose={() => setSettingsOpen(false)}>
                <div className="grid grid-cols-2 gap-3 pb-4">
                    <GenerationSettings config={effectiveConfig} model={model} updateConfig={updateConfig} openConfigDialog={openConfigDialog} />
                </div>
            </Drawer>
            <PromptSelectDialog open={promptDialogOpen} onOpenChange={setPromptDialogOpen} onSelect={setPrompt} />
            <AssetPickerModal open={assetPickerOpen} defaultTab="my-assets" onInsert={(payload) => void insertPickedAsset(payload)} onClose={() => setAssetPickerOpen(false)} />
            <Modal title={t("workbench.deleteLogs")} open={deleteConfirmOpen} onCancel={() => setDeleteConfirmOpen(false)} onOk={deleteSelectedLogs} okText={t("common.delete")} okButtonProps={{ danger: true }} cancelText={t("common.cancel")}>
                {t("workbench.deleteLogsConfirm", { count: selectedLogIds.length })}
            </Modal>
        </div>
    );
}

function GenerationSettings({ config, model, updateConfig, openConfigDialog }: { config: AiConfig; model: string; updateConfig: UpdateAiConfig; openConfigDialog: (shouldPromptContinue?: boolean) => void }) {
    const theme = canvasThemes[useThemeStore((state) => state.theme)];
    const { t } = useTranslation();

    return (
        <>
            <label className="col-span-2 block min-w-0 sm:col-span-1">
                <span className="mb-1.5 block text-sm font-semibold sm:mb-2 sm:text-base">{t("workbench.model")}</span>
                <ModelPicker config={config} value={model} onChange={(value) => updateConfig("videoModel", value)} capability="video" fullWidth onMissingConfig={() => openConfigDialog(false)} />
            </label>
            <div className="col-span-2">
                <VideoSettingsPanel config={config} onConfigChange={(key, value) => updateConfig(key, value)} theme={theme} showTitle={false} className="space-y-4" />
            </div>
        </>
    );
}

function ResultVideoCard({ video, onDownload, onSaveAsset }: { video: GeneratedVideo; onDownload: (video: GeneratedVideo) => void; onSaveAsset: (video: GeneratedVideo) => void }) {
    const { t } = useTranslation();
    return (
        <div className="overflow-hidden rounded-lg border border-stone-200 bg-background dark:border-stone-800">
            <video src={video.url} controls className="aspect-video w-full bg-black object-contain" />
            <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2 border-t border-stone-200 px-3 py-2.5 dark:border-stone-800">
                <div className="flex min-w-0 flex-wrap gap-x-2 gap-y-1 text-xs text-stone-500 dark:text-stone-400">
                    <span>
                        {video.width}x{video.height}
                    </span>
                    <span>{formatBytes(video.bytes)}</span>
                    <span>{formatDuration(video.durationMs)}</span>
                </div>
                <div className="flex shrink-0 gap-1">
                    <Button size="small" icon={<FolderPlus className="size-3.5" />} onClick={() => onSaveAsset(video)}>
                        {t("common.addToAssets")}
                    </Button>
                    <Button size="small" icon={<Download className="size-3.5" />} onClick={() => onDownload(video)}>
                        {t("common.download")}
                    </Button>
                </div>
            </div>
        </div>
    );
}

function PendingVideoCard({ error, progress }: { error?: string; progress?: string | number }) {
    const { t } = useTranslation();
    return (
        <div className="relative aspect-video overflow-hidden rounded-lg border border-dashed border-stone-300 bg-stone-50 dark:border-stone-700 dark:bg-stone-900">
            <div className="absolute inset-0 flex flex-col items-center justify-center gap-2 text-sm text-stone-500 dark:text-stone-400">
                <LoaderCircle className="size-6 animate-spin" />
                <span>{t("workbench.generating")}</span>
                {progress !== undefined ? <span>{progress}{typeof progress === "number" ? "%" : ""}</span> : null}
                {error ? <span className="max-w-full px-4 text-center text-xs">{error}</span> : null}
            </div>
        </div>
    );
}

function FailedVideoCard({ error, onRetry }: { error: string; onRetry: () => void }) {
    const { t } = useTranslation();
    return (
        <div className="overflow-hidden rounded-lg border border-red-200 bg-red-50 dark:border-red-950 dark:bg-red-950/20">
            <div className="flex aspect-video flex-col items-center justify-center gap-3 p-5 text-center">
                <div className="text-sm font-medium text-red-600 dark:text-red-300">{t("workbench.failed")}</div>
                <Typography.Paragraph ellipsis={{ rows: 4 }} className="!mb-0 !text-xs !text-red-500 dark:!text-red-300">
                    {error}
                </Typography.Paragraph>
            </div>
            <div className="flex justify-end border-t border-red-200 p-3 dark:border-red-950">
                <Button size="small" danger onClick={onRetry}>
                    {videoText("重新生成", "Generate again")}
                </Button>
            </div>
        </div>
    );
}

function StoppedVideoCard({ error, onRetry, onContinue }: { error: string; onRetry: () => void; onContinue?: () => void }) {
    return (
        <div className="overflow-hidden rounded-lg border border-stone-200 bg-stone-50 dark:border-stone-700 dark:bg-stone-900">
            <div className="flex aspect-video flex-col items-center justify-center gap-3 p-5 text-center">
                <div className="text-sm font-medium text-stone-700 dark:text-stone-200">{videoText("等待已暂停", "Waiting paused")}</div>
                <Typography.Paragraph ellipsis={{ rows: 4 }} className="!mb-0 !text-xs !text-stone-500 dark:!text-stone-400">
                    {error}
                </Typography.Paragraph>
            </div>
            <div className="flex justify-end gap-2 border-t border-stone-200 p-3 dark:border-stone-700">
                {onContinue ? <Button size="small" type="primary" onClick={onContinue}>{videoText("继续查询", "Continue querying")}</Button> : null}
                <Button size="small" onClick={onRetry}>
                    {videoText("重新生成", "Generate again")}
                </Button>
            </div>
        </div>
    );
}

function LogPanel({
    logs,
    selectedLogIds,
    activeLogId,
    onSelectedLogIdsChange,
    onCreateSession,
    onDeleteSelected,
    onPreviewLog,
}: {
    logs: GenerationLog[];
    selectedLogIds: string[];
    activeLogId?: string;
    onSelectedLogIdsChange: (ids: string[]) => void;
    onCreateSession: () => void;
    onDeleteSelected: () => void;
    onPreviewLog: (log: GenerationLog) => void;
}) {
    const { t } = useTranslation();
    const allSelected = Boolean(logs.length) && selectedLogIds.length === logs.length;
    const toggleAll = () => onSelectedLogIdsChange(allSelected ? [] : logs.map((log) => log.id));

    return (
        <>
            <div className="mb-3 flex items-center justify-between gap-3">
                <h2 className="text-base font-semibold">{t("workbench.logs")}</h2>
                <Tag className="m-0">{logs.length}</Tag>
            </div>
            <div className="mb-4 flex flex-wrap gap-2">
                <Button size="small" icon={<Plus className="size-3.5" />} onClick={onCreateSession}>
                    {t("workbench.new")}
                </Button>
                <Button size="small" icon={<CheckSquare className="size-3.5" />} disabled={!logs.length} onClick={toggleAll}>
                    {allSelected ? t("common.cancel") : t("workbench.selectAll")}
                </Button>
                <Button size="small" danger icon={<Trash2 className="size-3.5" />} disabled={!selectedLogIds.length} onClick={onDeleteSelected}>
                    {t("common.delete")}
                </Button>
            </div>
            <div className="space-y-3">
                {logs.map((log) => (
                    <LogCard key={log.id} log={log} selected={selectedLogIds.includes(log.id)} active={activeLogId === log.id} onSelectedChange={(checked) => onSelectedLogIdsChange(checked ? [...selectedLogIds, log.id] : selectedLogIds.filter((id) => id !== log.id))} onClick={() => onPreviewLog(log)} />
                ))}
                {!logs.length ? <div className="flex min-h-48 items-center justify-center rounded-lg border border-dashed border-stone-300 text-center text-sm text-stone-500 dark:border-stone-700">{t("workbench.noLogs")}</div> : null}
            </div>
        </>
    );
}

function LogCard({ log, selected, active, onSelectedChange, onClick }: { log: GenerationLog; selected: boolean; active: boolean; onSelectedChange: (checked: boolean) => void; onClick: () => void }) {
    const { t } = useTranslation();
    return (
        <button type="button" className={`block w-full rounded-lg border p-2 text-left transition ${active ? "border-stone-900 bg-blue-50 dark:border-stone-100 dark:bg-blue-950/20" : "border-stone-200 bg-background hover:bg-stone-50 dark:border-stone-800 dark:hover:bg-stone-900"}`} onClick={onClick}>
            <div className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-2">
                <Checkbox className="mt-0.5" checked={selected} onClick={(event) => event.stopPropagation()} onChange={(event) => onSelectedChange(event.target.checked)} />
                <div className="min-w-0">
                    <div className="truncate text-sm font-semibold leading-5">{log.title}</div>
                    <div className="mt-2 flex flex-wrap gap-1">
                        <Tag className="m-0 flex h-6 items-center rounded-md px-1.5 text-xs leading-none">{log.size}</Tag>
                        <Tag className="m-0 flex h-6 items-center rounded-md px-1.5 text-xs leading-none">{log.resolution}p</Tag>
                        <Tag className="m-0 flex h-6 items-center rounded-md px-1.5 text-xs leading-none">{log.seconds}s</Tag>
                    </div>
                </div>
                <div className="grid justify-items-end gap-2">
                    <Tag className="m-0 flex h-6 items-center rounded-md px-1.5 text-xs leading-none" color={log.status === "success" ? "blue" : log.status === "pending" ? "processing" : log.status === "stopped" ? "default" : "red"}>
                        {log.status === "stopped" || log.status === "interrupted" ? videoText("等待已暂停", "Waiting paused") : t(`workbench.${log.status === "success" ? "success" : log.status === "pending" ? "generating" : "failed"}`)}
                    </Tag>
                    <Tag className="m-0 flex h-6 items-center rounded-md px-1.5 text-xs leading-none" color="green">
                        {formatDuration(log.durationMs)}
                    </Tag>
                </div>
            </div>
        </button>
    );
}

async function readStoredLogs() {
    if (typeof window === "undefined") return [];
    try {
        const logs: GenerationLog[] = [];
        await logStore.iterate<GenerationLog, void>((value) => {
            logs.push(value);
        });
        return (await Promise.all(logs.map(normalizeLog))).sort((a, b) => (b.createdAt || 0) - (a.createdAt || 0));
    } catch {
        return [];
    }
}

async function normalizeLog(log: Partial<GenerationLog>): Promise<GenerationLog> {
    const video = log.video?.storageKey ? { ...log.video, url: await resolveMediaUrl(log.video.storageKey, log.video.url) } : log.video;
    const references = await Promise.all(
        (log.references || []).map(async (item) => {
            void ensureImagePreview(item.storageKey);
            return { ...item, dataUrl: await resolveImageUrl(item.storageKey, item.dataUrl) };
        }),
    );
    const config = normalizeLogConfig(log);
    return {
        id: log.id || nanoid(),
        createdAt: log.createdAt || Date.now(),
        title: log.title || log.model || i18n.t("workbench.untitled"),
        prompt: log.prompt || "",
        time: log.time || new Date().toLocaleString(i18n.resolvedLanguage, { hour12: false }),
        model: log.model || config.videoModel || "",
        config,
        references,
        referenceVideos: await restoreReferenceMedia(log.referenceVideos),
        referenceAudios: await restoreReferenceMedia(log.referenceAudios),
        durationMs: log.durationMs || 0,
        size: log.size || config.size || "",
        resolution: normalizeResolution(log.resolution || config.vquality || ""),
        seconds: log.seconds || config.videoSeconds || "",
        status: log.status === "pending" && !log.task ? "interrupted" : log.status || "success",
        task: log.task,
        video,
        error: log.status === "pending" && !log.task ? videoText("任务提交结果未确认，请先核对任务列表，不要重复提交。", "Submission outcome unknown. Check your task list before creating another task.") : log.error,
        failureSource: log.failureSource,
    };
}

function serializeLog(log: GenerationLog): GenerationLog {
    return {
        ...log,
        references: log.references.map((item) => ({ ...item, dataUrl: item.storageKey ? "" : item.dataUrl })),
        referenceVideos: serializeReferenceMedia(log.referenceVideos),
        referenceAudios: serializeReferenceMedia(log.referenceAudios),
        video: log.video?.storageKey ? { ...log.video, url: "" } : log.video,
    };
}

function moveListItem<T>(items: T[], index: number, offset: number) {
    const targetIndex = index + offset;
    if (targetIndex < 0 || targetIndex >= items.length) return items;
    const next = [...items];
    [next[index], next[targetIndex]] = [next[targetIndex], next[index]];
    return next;
}

function ReferenceOrderButtons({ index, total, onMove }: { index: number; total: number; onMove: (offset: number) => void }) {
    if (total <= 1) return null;
    return (
        <div className="absolute inset-x-1 bottom-1 flex justify-between">
            <Button size="small" className="!h-6 !w-6 !min-w-6 !rounded-full !bg-white/85 !p-0 !shadow-sm" icon={<ArrowLeft className="size-3" />} disabled={index <= 0} onClick={() => onMove(-1)} />
            <Button size="small" className="!h-6 !w-6 !min-w-6 !rounded-full !bg-white/85 !p-0 !shadow-sm" icon={<ArrowRight className="size-3" />} disabled={index >= total - 1} onClick={() => onMove(1)} />
        </div>
    );
}

function normalizeLogConfig(log: Partial<GenerationLog>): GenerationLogConfig {
    return {
        model: log.config?.model || log.model || "",
        videoModel: log.config?.videoModel || log.model || "",
        size: log.config?.size || log.size || "",
        vquality: normalizeResolution(log.config?.vquality || log.resolution || ""),
        videoSeconds: log.config?.videoSeconds || log.seconds || "",
        videoGenerateAudio: log.config?.videoGenerateAudio || "true",
        videoWatermark: log.config?.videoWatermark || "false",
        videoMode: normalizeVideoMode(log.config?.videoMode),
        firstFrameId: log.config?.firstFrameId,
        lastFrameId: log.config?.lastFrameId,
    };
}

function buildLog({ prompt, model, config, references, durationMs, status, task, video, error }: { prompt: string; model: string; config: AiConfig; references: ReferenceImage[]; durationMs: number; status: GenerationLog["status"]; task?: VideoGenerationTask; video?: GeneratedVideo; error?: string }): GenerationLog {
    const logConfig = {
        model: config.model,
        videoModel: config.videoModel,
        size: config.size,
        vquality: normalizeResolution(config.vquality),
        videoSeconds: config.videoSeconds,
        videoGenerateAudio: config.videoGenerateAudio,
        videoWatermark: config.videoWatermark,
        videoMode: normalizeVideoMode(config.videoMode),
        firstFrameId: config.firstFrameId,
        lastFrameId: config.lastFrameId,
    };
    return {
        id: nanoid(),
        createdAt: Date.now(),
        title: prompt.slice(0, 12) || i18n.t("workbench.untitled"),
        prompt,
        time: new Date().toLocaleString(i18n.resolvedLanguage, { hour12: false }),
        model,
        config: logConfig,
        references,
        referenceVideos: [],
        referenceAudios: [],
        durationMs,
        size: logConfig.size,
        resolution: logConfig.vquality,
        seconds: logConfig.videoSeconds,
        status,
        task,
        video,
        error,
    };
}

function buildVideoConfig(config: AiConfig, model: string): AiConfig {
    return {
        ...config,
        model,
        videoModel: model,
        size: isMiniMaxH3(model) ? config.size : normalizeVideoSize(config.size),
        videoSeconds: isMiniMaxH3(model) ? config.videoSeconds : normalizeVideoSeconds(config.videoSeconds),
        vquality: normalizeResolution(config.vquality),
        videoGenerateAudio: String(boolConfig(config.videoGenerateAudio, true)),
        videoWatermark: String(boolConfig(config.videoWatermark, false)),
        videoMode: normalizeVideoMode(config.videoMode),
    };
}

function normalizeVideoSeconds(value: string) {
    if (String(value).trim() === "-1") return "-1";
    return clampVideoSeconds(value);
}

function normalizeVideoSize(value: string) {
    return normalizeVideoSizeValue(value);
}

function normalizeResolution(value: string) {
    return normalizeVideoResolutionValue(value);
}

function localStopLabel() {
    return (i18n.resolvedLanguage || i18n.language || "en").startsWith("zh") ? "停止等待" : "Stop waiting";
}

function localStopMessage() {
    return (i18n.resolvedLanguage || i18n.language || "en").startsWith("zh") ? "已停止本地等待，服务端任务可能仍在继续。" : "Stopped waiting locally. The server task may still continue.";
}

function videoText(zh: string, en: string) {
    return (i18n.resolvedLanguage || i18n.language || "en").startsWith("zh") ? zh : en;
}

function throwIfAborted(signal: AbortSignal) {
    if (signal.aborted) throw new DOMException("Aborted", "AbortError");
}
