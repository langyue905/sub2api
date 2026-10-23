import { describe, expect, it, vi } from 'vitest'
import { normalizeVideoMode, selectVideoFrameImages } from '@/lib/video-references'
import { restoreReferenceMedia, serializeReferenceMedia } from '@/services/video-reference-history'
import { buildNodeGenerationContext, generationInputImages, buildNodeGenerationInputs } from '@/components/canvas/canvas-node-generation'
import { CanvasNodeType, type CanvasNodeData, type CanvasConnection } from '@/types/canvas'

vi.mock('@/i18n', () => ({ default: { t: (key: string) => key } }))
vi.mock('@/lib/canvas/node-registry', () => ({ getNodeDefinition: () => undefined }))
vi.mock('@/services/file-storage', () => ({ resolveMediaUrl: vi.fn(async (key, url) => key ? `blob:restored-${key}` : url) }))

describe('video references survive configuration and history', () => {
  it('restores stored audio/video with fresh URLs and retains public URLs and identifiers', async () => {
    const media = [{ id: 'video', url: 'blob:old', storageKey: 'video:1', name: 'clip' }, { id: 'audio', url: 'https://media.example.com/ref.mp3', name: 'audio' }]
    const saved = serializeReferenceMedia(media)
    expect(saved[0].url).toBe('')
    expect(media[0].url).toBe('blob:old')
    expect(await restoreReferenceMedia(saved)).toEqual([{ ...media[0], url: 'blob:restored-video:1' }, media[1]])
    expect(await restoreReferenceMedia()).toEqual([])
  })
  it('keeps explicit roles when reference order changes and rejects deleted roles', () => {
    const images = ['last', 'first'].map((id) => ({ id, name: id, type: 'image/png', dataUrl: `https://media.example.com/${id}.png` }))
    expect(normalizeVideoMode('first_last_frame')).toBe('first_last_frame')
    expect(selectVideoFrameImages(images, 'first_last_frame', 'first', 'last').map((image) => image.id)).toEqual(['first', 'last'])
    expect(selectVideoFrameImages(images, 'reference')).toBe(images)
    expect(() => selectVideoFrameImages(images.slice(0, 1), 'first_last_frame', 'first', 'last')).toThrow()
  })
})

describe('canvas node references', () => {
  const images = ['last', 'first'].map((id) => ({ id, type: CanvasNodeType.Image, title: id, metadata: { content: `https://media.example.com/${id}.png` } } as CanvasNodeData))
  const video = { id: 'clip', type: CanvasNodeType.Video, title: 'clip', metadata: { content: 'https://media.example.com/clip.mp4' } } as CanvasNodeData
  const audio = { id: 'sound', type: CanvasNodeType.Audio, title: 'sound', metadata: { content: 'https://media.example.com/sound.mp3' } } as CanvasNodeData
  const connections = ['first', 'last', 'clip', 'sound'].map((id) => ({ id, fromNodeId: id, toNodeId: 'target' } as CanvasConnection))
  const target = { id: 'target', type: CanvasNodeType.Config, metadata: { composerContent: 'transition' } } as CanvasNodeData
  const nodes = [...images, video, audio, target]
  it('uses explicit connected frames even without image mentions, leaving unrelated media unselected', () => {
    const context = buildNodeGenerationContext('target', nodes, connections, 'transition', { videoMode: 'first_last_frame', firstFrameId: 'first', lastFrameId: 'last' })
    expect(selectVideoFrameImages(context.referenceImages, 'first_last_frame', 'first', 'last').map((image) => image.id)).toEqual(['first', 'last'])
    expect(context.referenceVideos).toEqual([])
    expect(context.referenceAudios).toEqual([])
    expect(generationInputImages(buildNodeGenerationInputs('target', nodes, connections))).toHaveLength(2)
  })
  it('includes referenced video and audio for a composer and all media for an ordinary video node', () => {
    const composer = buildNodeGenerationContext('target', nodes, connections, '@[node:clip] @[node:sound]')
    expect(composer.referenceVideos[0].id).toBe('clip')
    expect(composer.referenceAudios[0].id).toBe('sound')
    const plain = buildNodeGenerationContext('target', [...images, video, audio, { ...target, type: CanvasNodeType.Video }], connections, 'transition')
    expect([plain.imageCount, plain.videoCount, plain.audioCount]).toEqual([2, 1, 1])
  })
})
