import { describe, expect, it, vi } from 'vitest'
import { CanvasNodeType, type CanvasNodeData } from '@/types/canvas'

vi.mock('@/i18n', () => ({ default: { t: (key: string) => key } }))
vi.mock('@/services/image-storage', () => ({}))
vi.mock('@/services/file-storage', () => ({}))
vi.mock('@/lib/canvas/canvas-node-factory', () => ({}))
const values = new Map<string, string>()
vi.stubGlobal('localStorage', { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => values.set(key, value), removeItem: (key: string) => values.delete(key) })
const { defaultConfig, useConfigStore } = await import('@/stores/use-config-store')
const { buildGenerationConfig } = await import('@/lib/canvas/canvas-generation-helpers')
const config = { ...defaultConfig, model: 'image-model', videoModel: 'channel::seedance-2.0', channels: [{ id: 'channel', name: 'test', baseUrl: 'https://example.com', apiKey: 'test-key', apiFormat: 'openai' as const, models: [{ name: 'MiniMax-H3', capability: 'video' as const }, { name: 'seedance-2.0', capability: 'video' as const }] }] }

describe('canvas video model selection', () => {
  it('uses the saved H3 model for the create request and preserves its settings', () => {
    const node = { type: CanvasNodeType.Video, metadata: { model: 'channel::MiniMax-H3', seconds: '15', vquality: '2k', size: '1:1' } } as CanvasNodeData
    expect(buildGenerationConfig(config, node, 'video')).toMatchObject({ model: 'channel::MiniMax-H3', videoModel: 'channel::MiniMax-H3', videoSeconds: '15', vquality: '2k', size: '1:1' })
  })
  it('keeps unavailable saved models so generation cannot silently switch provider', () => {
    const node = { type: CanvasNodeType.Video, metadata: { model: 'removed::MiniMax-H3' } } as CanvasNodeData
    const selected = buildGenerationConfig(config, node, 'video')
    expect(selected.videoModel).toBe('removed::MiniMax-H3')
    expect(useConfigStore.getState().isAiConfigReady(selected, selected.model)).toBe(false)
  })
  it('uses the configured default for a new video node', () => {
    expect(buildGenerationConfig(config, undefined, 'video').videoModel).toBe(config.videoModel)
  })
})
