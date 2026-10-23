import type { ReferenceImage } from "@/types/image";
import i18n from "@/i18n";

export function VideoFrameSelector({ images, firstFrameId, lastFrameId, onChange, onUpload, disabled = false }: {
    images: ReferenceImage[]; firstFrameId?: string; lastFrameId?: string;
    onChange: (patch: { firstFrameId?: string; lastFrameId?: string }) => void;
    onUpload?: (role: "firstFrameId" | "lastFrameId", file: File) => Promise<void>;
    disabled?: boolean;
}) {
    const zh = (i18n.resolvedLanguage || i18n.language).startsWith("zh");
    return <div className="my-2 grid min-w-0 grid-cols-2 gap-2" onMouseDown={(event) => event.stopPropagation()}>
        {(["firstFrameId", "lastFrameId"] as const).map((role) => {
            const value = (role === "firstFrameId" ? firstFrameId : lastFrameId) || "";
            const image = images.find((item) => item.id === value);
            const label = role === "firstFrameId" ? (zh ? "首帧" : "First frame") : (zh ? "尾帧" : "Last frame");
            return <div key={role} className="min-w-0 rounded-lg border border-stone-400/30 p-2">
                <label className="block text-xs font-medium">{label}
                    <select disabled={disabled} aria-label={label} className="mt-1 w-full min-w-0 rounded border border-stone-400/40 bg-transparent p-1 text-xs" value={image ? value : ""} onChange={(event) => onChange({ [role]: event.target.value })}>
                        <option value="">{zh ? "请选择图片" : "Select an image"}</option>
                        {images.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
                    </select>
                </label>
                {image ? <div className="relative mt-1"><img src={image.dataUrl || image.url} alt={label} className="h-16 w-full rounded object-contain" /><button type="button" aria-label={zh ? `清除${label}` : `Clear ${label}`} className="absolute right-0 top-0 rounded bg-black/70 px-1 text-white" onClick={() => onChange({ [role]: "" })}>×</button></div> : null}
                {onUpload ? <label className="mt-1 block cursor-pointer text-xs underline">{zh ? `上传${label}` : `Upload ${label}`}<input disabled={disabled} className="hidden" type="file" accept="image/*" onChange={(event) => { const file = event.target.files?.[0]; if (file) void onUpload(role, file); event.target.value = ""; }} /></label> : null}
            </div>;
        })}
    </div>;
}
