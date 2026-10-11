import { App, Button, Form, Input, Modal, Select } from "antd";
import { Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { ModelPicker } from "@/components/model-picker";
import { LOCKED_API_BASE_URL, normalizeChannelModels, useConfigStore, type ModelCapability } from "@/stores/use-config-store";

const capabilities: Array<{ value: ModelCapability; label: string }> = [{ value: "image", label: "图片" }, { value: "video", label: "视频" }, { value: "text", label: "文本" }, { value: "audio", label: "音频" }];
export function AppConfigPanel({ showDoneButton = false }: { showDoneButton?: boolean }) {
    const { message } = App.useApp(); const { t } = useTranslation(); const config = useConfigStore((state) => state.config); const updateConfig = useConfigStore((state) => state.updateConfig); const shouldPromptContinue = useConfigStore((state) => state.shouldPromptContinue); const setConfigDialogOpen = useConfigStore((state) => state.setConfigDialogOpen); const clearPromptContinue = useConfigStore((state) => state.clearPromptContinue);
    const [modelName, setModelName] = useState(""); const [capability, setCapability] = useState<ModelCapability>("image"); const models = config.channels[0]?.models || [];
    const addModel = () => { const name = modelName.trim(); if (!name || models.some((model) => model.name === name)) return; updateConfig("channels", [{ ...config.channels[0], models: normalizeChannelModels([...models, { name, capability }]) }]); setModelName(""); };
    const removeModel = (name: string) => updateConfig("channels", [{ ...config.channels[0], models: models.filter((model) => model.name !== name) }]);
    const finish = () => { setConfigDialogOpen(false); if (config.apiKey.trim() && models.length) message.success(t(shouldPromptContinue ? "config.savedContinue" : "config.saved")); clearPromptContinue(); };
    return <Form layout="vertical" requiredMark={false}>
        <Form.Item label="API 地址" extra="已固定为本站接口地址"><Input value={`${LOCKED_API_BASE_URL}/v1`} disabled /></Form.Item>
        <Form.Item label="本站 API Key" required><Input.Password value={config.apiKey} autoComplete="off" onChange={(event) => updateConfig("apiKey", event.target.value)} /></Form.Item>
        <div className="mb-3 text-sm font-semibold">模型</div><div className="mb-3 flex flex-wrap gap-2"><Input className="min-w-[180px] flex-1" value={modelName} placeholder="输入模型名称" onChange={(event) => setModelName(event.target.value)} onPressEnter={addModel} /><Select className="w-24" value={capability} options={capabilities} onChange={setCapability} /><Button icon={<Plus className="size-4" />} onClick={addModel}>添加</Button></div>
        <div className="mb-5 space-y-2">{models.map((model) => <div key={model.name} className="flex items-center justify-between gap-3 rounded-lg border border-stone-200 px-3 py-2 dark:border-stone-800"><span className="min-w-0 truncate text-sm">{model.name}</span><span className="text-xs text-stone-500">{capabilities.find((item) => item.value === model.capability)?.label}</span><Button type="text" danger size="small" icon={<Trash2 className="size-4" />} aria-label={`删除 ${model.name}`} onClick={() => removeModel(model.name)} /></div>)}</div>
        <div className="mb-3 text-sm font-semibold">默认模型</div><div className="grid gap-3 md:grid-cols-2">{(["image", "video", "text", "audio"] as ModelCapability[]).map((type) => { const key = `${type}Model` as "imageModel" | "videoModel" | "textModel" | "audioModel"; return <Form.Item key={type} label={capabilities.find((item) => item.value === type)?.label} className="mb-0"><ModelPicker config={config} value={config[key]} capability={type} fullWidth onChange={(value) => updateConfig(key, value)} /></Form.Item>; })}</div>
        <Form.Item label="系统提示词" className="mt-4"><Input.TextArea rows={3} value={config.systemPrompt} onChange={(event) => updateConfig("systemPrompt", event.target.value)} /></Form.Item>{showDoneButton ? <div className="flex justify-end"><Button type="primary" onClick={finish}>{t("common.done")}</Button></div> : null}
    </Form>;
}
export function AppConfigModal() { const { t } = useTranslation(); const open = useConfigStore((state) => state.isConfigOpen); const setOpen = useConfigStore((state) => state.setConfigDialogOpen); return <Modal title={t("config.title")} open={open} footer={null} width={760} onCancel={() => setOpen(false)} destroyOnHidden><AppConfigPanel showDoneButton /></Modal>; }
