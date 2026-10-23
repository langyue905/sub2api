import { useRef, useState } from "react";
import { App, Button } from "antd";
import { nanoid } from "nanoid";
import { uploadMediaFile } from "@/services/file-storage";
import type { ReferenceAudio, ReferenceVideo } from "@/types/media";
import i18n from "@/i18n";

export function VideoMediaReferences({ kind, items, onChange, onBusyChange }: {
    kind: "video" | "audio"; items: (ReferenceVideo | ReferenceAudio)[];
    onChange: (items: (ReferenceVideo | ReferenceAudio)[]) => void;
    onBusyChange: (busy: boolean) => void;
}) {
    const { message } = App.useApp();
    const input = useRef<HTMLInputElement>(null);
    const [busy, setBusy] = useState(false);
    const busyRef = useRef(false);
    const zh = (i18n.resolvedLanguage || i18n.language).startsWith("zh");
    const label = kind === "video" ? (zh ? "参考视频" : "Reference videos") : (zh ? "参考音频" : "Reference audio");
    const addFiles = async (files: File[]) => {
        if (busyRef.current) return;
        busyRef.current = true;
        setBusy(true); onBusyChange(true);
        const next = [...items];
        try {
            if (files.length > 3 - items.length) message.warning(zh ? `${label}最多3个` : "Maximum 3 references per type");
            for (const file of files.slice(0, Math.max(0, 3 - items.length))) {
                if (!file.type.startsWith(`${kind}/`)) throw new Error(zh ? "请选择匹配类型的素材文件" : "Select a matching media file");
                if (!file.size || file.size > 32 * 1024 * 1024) throw new Error(zh ? "单个素材不能为空，最大32 MiB" : "Each reference must be non-empty and at most 32 MiB");
                const stored = await uploadMediaFile(file, kind);
                next.push({ id: nanoid(), name: file.name, type: stored.mimeType, ...stored });
            }
        } catch (error) {
            message.error(error instanceof Error ? error.message : String(error));
        } finally {
            onChange(next); busyRef.current = false; setBusy(false); onBusyChange(false);
        }
    };
    return <section className="min-w-0 rounded-lg border border-stone-400/30 p-2">
        <div className="mb-2 flex flex-wrap items-center justify-between gap-2"><span className="text-sm font-medium">{label} ({items.length}/3)</span><Button size="small" loading={busy} disabled={items.length >= 3} onClick={() => input.current?.click()}>{zh ? "上传" : "Upload"}</Button></div>
        <input ref={input} type="file" accept={`${kind}/*`} multiple className="hidden" onChange={(event) => { void addFiles(Array.from(event.target.files || [])); event.target.value = ""; }} />
        <div className="space-y-2">{items.map((item) => <div key={item.id} className="min-w-0 rounded border border-stone-400/20 p-2">
            <div className="mb-1 flex min-w-0 items-center justify-between gap-2"><span className="truncate text-xs" title={item.name}>{item.name}</span><Button size="small" danger disabled={busy} onClick={() => onChange(items.filter((ref) => ref.id !== item.id))}>{zh ? "移除" : "Remove"}</Button></div>
            {kind === "video" ? <video src={item.url} controls preload="metadata" className="max-h-32 w-full rounded" /> : <audio src={item.url} controls preload="metadata" className="w-full min-w-0" />}
        </div>)}</div>
    </section>;
}
