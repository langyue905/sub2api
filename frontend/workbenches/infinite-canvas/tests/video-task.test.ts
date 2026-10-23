import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import axios from 'axios'
import type { AiConfig } from '@/stores/use-config-store'
import { createVideoGenerationTask, pollVideoGenerationTask, waitForVideoGenerationTask, storeGeneratedVideo } from '@/services/api/video'
import { canContinueVideoTask, shouldResumeVideoTask } from '@/services/video-task-history'
import { uploadMediaFile } from '@/services/file-storage'

vi.mock('axios', () => ({ default: {
  get: vi.fn(), post: vi.fn(), isCancel: () => false,
  isAxiosError: (error: { isAxiosError?: boolean }) => Boolean(error?.isAxiosError),
} }))
vi.mock('@/i18n', () => ({ default: { t: (key: string) => key } }))
vi.mock('@/services/file-storage', () => ({ getMediaBlob: vi.fn(), uploadMediaFile: vi.fn() }))
vi.mock('@/services/image-storage', () => ({ getImageBlob: vi.fn(), imageToDataUrl: vi.fn() }))
vi.mock('@/stores/use-config-store', () => ({
  buildApiUrl: (base: string, path: string) => `${base}/v1${path}`,
  modelOptionName: (model: string) => model,
  resolveModelRequestConfig: (config: AiConfig, model: string) => ({ ...config, model }),
}))

const config = { apiKey: 'test-only-key', baseUrl: 'https://gateway.example', model: 'seedance-2.0', videoModel: 'seedance-2.0', videoSeconds: '7', vquality: '720', size: '16:9', videoGenerateAudio: 'true' } as AiConfig
const task = { id: 'vidtask_existing', provider: 'openai' as const, model: 'seedance-2.0' }
const url = 'https://video.example/result.mp4'
const video = new Blob(['video-bytes'], { type: 'video/mp4' })
const get = vi.mocked(axios.get)
const post = vi.mocked(axios.post)

beforeEach(() => vi.resetAllMocks())
afterEach(() => vi.useRealTimers())

describe('asynchronous video tasks', () => {
  it.each([
    ['768', '1:1', '1024x1024', '768p'],
    ['1080', '1:1', '1440x1440', '1080p'],
    ['2k', '21:9', '2K', '2k'],
    ['4k', '9:16', '4K', '4k'],
  ])('submits H3 %s with its exact size and billing tier', async (vquality, size, expectedSize, resolution) => {
    post.mockResolvedValue({ data: { id: 'wrapper-id', data: { task_id: 'h3-task', id: 'secondary-id' } } })
    const image = { url: 'https://media.example.com/reference.png' }
    const references = vquality.endsWith('k') ? [{ ...image, id: 'test-reference', name: 'reference.png', type: 'image/png', dataUrl: '' }] : []
    const created = await createVideoGenerationTask({ ...config, model: 'MiniMax-H3', videoModel: 'MiniMax-H3', videoSeconds: '15', vquality, size }, 'camera move', references)
    expect(created.id).toBe('h3-task')
    expect(post).toHaveBeenCalledTimes(1)
    const body = post.mock.calls[0][1] as Record<string, unknown>
    expect(body).toEqual({ model: 'MiniMax-H3', prompt: 'camera move', seconds: 15, resolution, size: expectedSize, ...(vquality.endsWith('k') ? { aspect_ratio: size, images: [image.url] } : {}) })
  })

  it('submits explicitly assigned H3 frames in role order and preserves audio/video references', async () => {
    post.mockResolvedValue({ data: { task_id: 'h3-frames' } })
    const references = ['last', 'unused', 'first'].map((id) => ({ id, name: id, type: 'image/png', dataUrl: `https://media.example.com/${id}.png` }))
    const videos = [1, 2, 3].map((id) => ({ id: String(id), name: 'video', type: 'video/mp4', url: `https://media.example.com/${id}.mp4` }))
    const audios = [1, 2, 3].map((id) => ({ id: String(id), name: 'audio', type: 'audio/mpeg', url: `https://media.example.com/${id}.mp3` }))
    await createVideoGenerationTask({ ...config, videoModel: 'MiniMax-H3', videoMode: 'first_last_frame', firstFrameId: 'first', lastFrameId: 'last', videoSeconds: '4', vquality: '768' }, 'transition', references, { videos, audios })
    expect(post).toHaveBeenCalledTimes(1)
    expect(post.mock.calls[0][1]).toEqual({ model: 'MiniMax-H3', prompt: 'transition', seconds: 4, resolution: '768p', size: '1376x768', mode: 'first_last_frame', images: ['https://media.example.com/first.png', 'https://media.example.com/last.png'], reference_videos: videos.map((v) => v.url), reference_audios: audios.map((a) => a.url) })
  })

  it('rejects a removed frame before uploading or submitting', async () => {
    await expect(createVideoGenerationTask({ ...config, videoModel: 'MiniMax-H3', videoMode: 'first_last_frame', firstFrameId: 'removed', lastFrameId: 'last', videoSeconds: '4', vquality: '768' }, 'transition', [{ id: 'last', name: 'last', type: 'image/png', dataUrl: 'https://media.example.com/last.png' }])).rejects.toThrow(/首帧和尾帧/)
    expect(post).not.toHaveBeenCalled()
  })

  it('submits all 933 ordinary references without a frame mode', async () => {
    post.mockResolvedValue({ data: { id: 'h3-933' } })
    const images = Array.from({ length: 9 }, (_, i) => ({ id: String(i), name: 'image', type: 'image/png', dataUrl: `https://media.example.com/${i}.png` }))
    const media = Array.from({ length: 3 }, (_, i) => ({ id: String(i), name: 'media', type: 'video/mp4', url: `https://media.example.com/${i}.mp4` }))
    await createVideoGenerationTask({ ...config, videoModel: 'MiniMax-H3', videoMode: 'reference', videoSeconds: '4', vquality: '768' }, 'transition', images, { videos: media, audios: media.map((m) => ({ ...m, type: 'audio/mpeg', url: m.url.replace('.mp4', '.mp3') })) })
    expect(post).toHaveBeenCalledTimes(1)
    expect(post.mock.calls[0][1]).toMatchObject({ images: images.map((i) => i.dataUrl), reference_videos: media.map((v) => v.url), reference_audios: media.map((v) => v.url.replace('.mp4', '.mp3')) })
    expect(post.mock.calls[0][1]).not.toHaveProperty('mode')
  })

  it.each(['2k', '4k'])('rejects image-free H3 %s before uploading or submitting', async (vquality) => {
    await expect(createVideoGenerationTask({ ...config, model: 'MiniMax-H3', videoModel: 'MiniMax-H3', videoSeconds: '4', vquality }, 'camera move')).rejects.toThrow(/超分至少需要一张参考图/)
    expect(post).not.toHaveBeenCalled()
  })

  it.each([
    { vquality: '480' }, { vquality: '2160p' }, { videoSeconds: '3' },
    { videoSeconds: '16' }, { size: 'auto' }, { vquality: '768', size: '1440x1440' },
  ])('rejects invalid persisted H3 settings before creating: %j', async (settings) => {
    await expect(createVideoGenerationTask({ ...config, model: 'MiniMax-H3', videoModel: 'MiniMax-H3', videoSeconds: '5', vquality: '2k', size: '16:9', ...settings }, 'camera move')).rejects.toThrow()
    expect(post).not.toHaveBeenCalled()
  })

  it('resumes an H3 task and prefers result_url without resubmission', async () => {
    const preferred = 'https://video.example/preferred.mp4'
    get.mockResolvedValueOnce({ data: { data: { status: 'succeeded', result_url: preferred, video_url: url, url } } })
      .mockResolvedValueOnce({ data: video })
    const state = await pollVideoGenerationTask({ ...config, videoModel: 'MiniMax-H3' }, { ...task, model: 'MiniMax-H3' })
    expect(state).toMatchObject({ status: 'completed', result: { url: preferred } })
    expect(post).not.toHaveBeenCalled()
  })

  it('keeps the same task beyond the former five-minute cutoff and POSTs only once', async () => {
    vi.useFakeTimers()
    post.mockResolvedValue({ data: { id: task.id, status: 'queued' } })
    let queries = 0
    get.mockImplementation(async (path) => {
      if (String(path).endsWith('/content')) return { data: video }
      return { data: ++queries <= 130 ? { status: 'in_progress', progress: '99%', data: [{ url }] } : { status: 'succeeded', data: [{ url }] } }
    })
    const created = await createVideoGenerationTask(config, 'slow camera move')
    const pending = vi.fn()
    // Fingerprint validation is covered separately with real WebCrypto.
    const result = waitForVideoGenerationTask(config, { ...created, requestKey: undefined }, { onPending: pending })
    await vi.runAllTimersAsync()
    expect((await result).blob).toBe(video)
    expect(pending).toHaveBeenCalledTimes(130)
    expect(post).toHaveBeenCalledTimes(1)
    expect(get.mock.calls.filter(([path]) => String(path).endsWith('/content'))).toHaveLength(1)
    expect(get.mock.calls.every(([path]) => String(path).includes(task.id))).toBe(true)
  })

  it.each(['queued', 'in_progress'])('does not deliver a URL while status is %s / 99%%', async (status) => {
    get.mockResolvedValue({ data: { status, progress: '99%', data: [{ url }] } })
    expect(await pollVideoGenerationTask(config, task)).toEqual({ status: 'pending', progress: '99%' })
    expect(get).toHaveBeenCalledTimes(1)
  })

  it('reconnects after query errors without creating another task', async () => {
    vi.useFakeTimers()
    get.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({ data: { status: 'queued' } })
      .mockResolvedValueOnce({ data: { status: 'succeeded', data: [{ url }] } }).mockResolvedValueOnce({ data: video })
    const onQueryError = vi.fn()
    const result = waitForVideoGenerationTask(config, task, { onQueryError })
    await vi.runAllTimersAsync()
    expect((await result).url).toBe(url)
    expect(onQueryError).toHaveBeenCalledTimes(1)
    expect(post).not.toHaveBeenCalled()
  })

  it('can stop locally then continue querying the original ID', async () => {
    vi.useFakeTimers()
    const controller = new AbortController()
    get.mockResolvedValueOnce({ data: { status: 'queued' } })
    const outcome = waitForVideoGenerationTask(config, task, { signal: controller.signal }).catch((error) => error)
    await vi.advanceTimersByTimeAsync(0)
    controller.abort()
    expect((await outcome).name).toBe('AbortError')
    get.mockResolvedValueOnce({ data: { status: 'succeeded', data: [{ url }] } }).mockResolvedValueOnce({ data: video })
    expect((await waitForVideoGenerationTask(config, task)).url).toBe(url)
    expect(post).not.toHaveBeenCalled()
  })

  it.each(['failed', 'expired'])('treats HTTP 200 / %s as terminal and preserves media-storage errors', async (status) => {
    get.mockResolvedValue({ data: { status, error: { code: 'media_storage_failed', message: 'Storage failed' } } })
    await expect(waitForVideoGenerationTask(config, task)).rejects.toMatchObject({ name: 'VideoTaskFailed', message: 'Storage failed' })
    expect(get).toHaveBeenCalledTimes(1)
    expect(post).not.toHaveBeenCalled()
  })

  it('reads data[].url and JSON content responses, retaining URL if CDN download fails', async () => {
    get.mockResolvedValueOnce({ data: { code: 0, data: { status: 'succeeded', data: [{}, { url }] } } })
      .mockResolvedValueOnce({ data: new Blob([JSON.stringify({ url, content_type: 'video/mp4' })], { type: 'application/json' }) })
      .mockRejectedValueOnce(new Error('CORS'))
    expect(await pollVideoGenerationTask(config, task)).toEqual({ status: 'completed', result: { url, mimeType: 'video/mp4' } })
  })

  it('does not mistake JSON content errors for video bytes', async () => {
    get.mockResolvedValueOnce({ data: { status: 'succeeded' } })
      .mockResolvedValueOnce({ data: new Blob(['{"error":"media temporarily unavailable"}'], { type: 'application/json' }) })
    await expect(pollVideoGenerationTask(config, task)).rejects.toThrow('media temporarily unavailable')
    expect(post).not.toHaveBeenCalled()
  })

  it('retains the URL when the content endpoint and local storage both fail', async () => {
    get.mockResolvedValueOnce({ data: { status: 'succeeded', data: [{ url }] } })
      .mockRejectedValueOnce(new Error('503')).mockResolvedValueOnce({ data: video })
    const state = await pollVideoGenerationTask(config, task)
    expect(state.status).toBe('completed')
    vi.mocked(uploadMediaFile).mockRejectedValue(new Error('quota exceeded'))
    if (state.status === 'completed') expect((await storeGeneratedVideo(state.result)).url).toBe(url)
  })

  it('refuses to query with a changed API permission domain', async () => {
    post.mockResolvedValue({ data: { id: task.id, status: 'queued' } })
    const created = await createVideoGenerationTask(config, 'camera move')
    expect(created.requestKey).toMatch(/^[a-f0-9]{64}$/)
    await expect(waitForVideoGenerationTask({ ...config, apiKey: 'different-key' }, created)).rejects.toMatchObject({ name: 'VideoTaskConfigurationChanged' })
    expect(get).not.toHaveBeenCalled()
    expect(post).toHaveBeenCalledTimes(1)
  })

  it.each([undefined, 503])('marks uncertain submission as unknown without retrying (status %s)', async (status) => {
    post.mockRejectedValue({ isAxiosError: true, response: status ? { status } : undefined })
    await expect(createVideoGenerationTask(config, 'camera move')).rejects.toMatchObject({ name: 'VideoSubmissionUncertain' })
    expect(post).toHaveBeenCalledTimes(1)
  })

  it('does not retry a successful response missing its task ID', async () => {
    post.mockResolvedValue({ data: { status: 'queued' } })
    await expect(createVideoGenerationTask(config, 'camera move')).rejects.toMatchObject({ name: 'VideoSubmissionUncertain' })
    expect(post).toHaveBeenCalledTimes(1)
  })
})

describe('reload and history recovery', () => {
  it('automatically resumes pending and legacy local timeouts, preserving manual stops', () => {
    expect(shouldResumeVideoTask({ status: 'pending', task })).toBe(true)
    expect(shouldResumeVideoTask({ status: 'failed', task, error: '视频生成超时' })).toBe(true)
    expect(canContinueVideoTask({ status: 'stopped', task })).toBe(true)
    expect(shouldResumeVideoTask({ status: 'stopped', task })).toBe(false)
    expect(canContinueVideoTask({ status: 'interrupted', task })).toBe(true)
  })
  it('does not resume explicit upstream failures or unknown submissions', () => {
    expect(shouldResumeVideoTask({ status: 'failed', task, error: 'timeout', failureSource: 'upstream' })).toBe(false)
    expect(canContinueVideoTask({ status: 'failed', task, error: 'media_storage_failed', failureSource: 'upstream' })).toBe(false)
    expect(shouldResumeVideoTask({ status: 'pending' })).toBe(false)
  })
})
