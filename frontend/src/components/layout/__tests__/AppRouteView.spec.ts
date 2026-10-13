import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, reactive } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import AppRouteView from '../AppRouteView.vue'

const session = vi.hoisted(() => ({ current: null as unknown }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => session.current }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: defineComponent({ setup: (_, { slots }) => () => h('main', slots.default?.()) })
}))

const mounted: ReturnType<typeof mount>[] = []
afterEach(() => {
  mounted.forEach(wrapper => wrapper.unmount())
  mounted.length = 0
})

async function setup() {
  const auth = reactive({ isAuthenticated: true, user: { id: 1 } as { id: number } | null })
  session.current = auth
  const Page = defineComponent({ render: () => h('p', 'Other page') })
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/dashboard', component: Page },
      { path: '/keys', component: Page },
      { path: '/login', component: Page },
      { path: '/canvas-workbench', alias: '/canvas-workbench/', name: 'CanvasWorkbench', component: Page }
    ]
  })
  await router.push('/dashboard')
  await router.isReady()
  const wrapper = mount(AppRouteView, { global: { plugins: [router] }, attachTo: document.body })
  mounted.push(wrapper)
  const go = async (path: string) => {
    await router.push(path)
    await flushPromises()
  }
  return { auth, wrapper, go }
}

describe('Canvas workbench navigation', () => {
  it('keeps the same connected iframe and document while visiting other main menus', async () => {
    const { wrapper, go } = await setup()
    expect(wrapper.find('iframe').exists()).toBe(false)
    await go('/canvas-workbench')
    const frame = wrapper.get('iframe').element as HTMLIFrameElement
    const parent = frame.parentElement
    const frameDocument = frame.contentDocument!
    frameDocument.open()
    frameDocument.write('<!doctype html><html><body></body></html>')
    frameDocument.close()
    const draft = frameDocument.createElement('textarea')
    draft.value = 'Keep my unfinished video prompt'
    frameDocument.body.append(draft)

    for (const destination of ['/dashboard', '/keys']) {
      await go(destination)
      expect(wrapper.text()).toContain('Other page')
      expect(wrapper.get('iframe').isVisible()).toBe(false)
      expect(frame.isConnected).toBe(true)
      expect(frame.parentElement).toBe(parent)
      await go('/canvas-workbench/')
      expect(wrapper.get('iframe').element).toBe(frame)
      expect(wrapper.get('iframe').isVisible()).toBe(true)
      expect(frame.contentDocument).toBe(frameDocument)
      expect(frame.contentDocument!.querySelector('textarea')?.value).toBe(draft.value)
      expect(wrapper.text()).not.toContain('Other page')
    }
  })

  it('discards the active iframe on logout and when the user changes', async () => {
    const { auth, wrapper, go } = await setup()
    await go('/canvas-workbench')
    const firstFrame = wrapper.get('iframe').element
    await go('/dashboard')
    auth.isAuthenticated = false
    auth.user = null
    await flushPromises()
    expect(wrapper.find('iframe').exists()).toBe(false)
    auth.user = { id: 2 }
    auth.isAuthenticated = true
    await flushPromises()
    expect(wrapper.find('iframe').exists()).toBe(false)
    await go('/canvas-workbench')
    const secondFrame = wrapper.get('iframe').element
    expect(secondFrame).not.toBe(firstFrame)
    auth.user = { id: 3 }
    await flushPromises()
    expect(wrapper.get('iframe').element).not.toBe(secondFrame)
  })
})
