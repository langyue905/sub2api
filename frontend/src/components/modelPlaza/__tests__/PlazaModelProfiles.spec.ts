import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PlazaModelCard from '../PlazaModelCard.vue'
import PlazaModelDetail from '../PlazaModelDetail.vue'
import ModelPlazaProfilesEditor from '@/components/admin/ModelPlazaProfilesEditor.vue'
import type { ModelPlazaGroup, PlazaModel, PlazaModelProfile } from '@/api/modelPlaza'

vi.mock('@/stores/app', () => ({ useAppStore: () => ({ cachedPublicSettings: null }) }))
vi.mock('vue-i18n', async () => {
  const { default: dashboard } = await import('@/i18n/locales/zh/dashboard')
  const { default: settings } = await import('@/i18n/locales/zh/admin/settings')
  const messages = { ...dashboard, admin: settings }
  return {
    useI18n: () => ({
      t: (key: string, params: Record<string, unknown> = {}) => {
        const value = key.split('.').reduce<unknown>((node, part) =>
          node && typeof node === 'object' ? (node as Record<string, unknown>)[part] : undefined, messages)
        return typeof value === 'string'
          ? value.replace(/\{(\w+)\}/g, (_, name: string) => String(params[name] ?? `{${name}}`))
          : key
      }
    })
  }
})
const profile: PlazaModelProfile = {
  model: 'seedance-2.0', summary: '视频模型介绍', description: '**详细说明** <img src=x onerror="alert(1)"><script>alert(1)</script>',
  tags: ['视频'], specs: [{ label: '比例', value: '16:9' }]
}
const model: PlazaModel = {
  name: 'seedance-2.0', platform: 'openai', official_pricing: null, profile,
  pricing: { billing_mode: 'video', input_price: null, output_price: null, cache_write_price: null,
    cache_read_price: null, image_input_price: null, image_output_price: null, per_request_price: 0.1,
    intervals: [{ min_tokens: 0, max_tokens: null, tier_label: '720p', per_request_price: 0.1,
      input_price: null, output_price: null, cache_write_price: null, cache_read_price: null }] }
}
const group = { platform: 'openai', rate_multiplier: 0.2, user_rate_multiplier: 0.1,
  video_rate_independent: true, video_rate_multiplier: 0.5, models: [model] } as ModelPlazaGroup

describe('model descriptions and card pricing', () => {
  it('uses independent video rates and per-second units without changing stored prices', async () => {
    const wrapper = mount(PlazaModelCard, { props: { model, group } })
    expect(wrapper.text()).toContain('$0.05 / 秒')
    expect(wrapper.text()).toContain('视频模型介绍')
    expect(wrapper.text()).toContain('720p')
    await wrapper.find('button').trigger('click')
    expect(wrapper.emitted('select')?.[0]).toEqual([model])
    expect(model.pricing?.intervals[0].per_request_price).toBe(0.1)
    wrapper.unmount()
  })

  it('renders metadata and sanitizes Markdown in model details', () => {
    const wrapper = mount(PlazaModelDetail, { props: { model, group }, global: {
      stubs: { BaseDialog: { template: '<div><slot /></div>' } }
    } })
    expect(wrapper.text()).toContain('16:9')
    expect(wrapper.find('.model-description strong').text()).toBe('详细说明')
    expect(wrapper.find('.model-description').html()).not.toMatch(/<script|onerror=/)
    expect(wrapper.text()).toContain('/ 秒')
    expect(wrapper.text()).not.toContain('/ 次')
    wrapper.unmount()
  })

  it('edits existing descriptions without mutating the supplied settings', async () => {
    const wrapper = mount(ModelPlazaProfilesEditor, { props: { modelValue: [profile] } })
    await wrapper.find('button[aria-expanded]').trigger('click')
    await wrapper.find('textarea').setValue('新的简介')
    const emitted = wrapper.emitted('update:modelValue')!.at(-1)![0] as PlazaModelProfile[]
    expect(emitted[0].summary).toBe('新的简介')
    expect(emitted[0].specs).toEqual(profile.specs)
    expect(profile.summary).toBe('视频模型介绍')
    wrapper.unmount()
  })
})
