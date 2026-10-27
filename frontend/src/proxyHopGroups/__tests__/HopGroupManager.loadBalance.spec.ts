import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import HopGroupManager from '../HopGroupManager.vue'

const { list, get, create } = vi.hoisted(() => ({ list: vi.fn(), get: vi.fn(), create: vi.fn() }))
vi.mock('../api', () => ({ hopGroupsAPI: { list, get, create, update: vi.fn(), remove: vi.fn() } }))
vi.mock('@/api/admin/proxies', () => ({ proxiesAPI: { getAll: vi.fn().mockResolvedValue([]) } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } }),
}))

const group = {
  id: 3, name: 'lb', strategy: 'load-balance', test_url: 'https://www.gstatic.com/generate_204',
  interval_seconds: 60, tolerance_ms: 50, subscription_urls: [], subscription_count: 0,
  member_proxy_ids: [], subscription_update_interval: 3600, status: 'active',
  created_at: '', updated_at: '',
  runtime: { selected: '', providers: [], nodes: [{ name: 'node-a', alive: true, delay_ms: 12, active_connections: 4 }] },
}
const mountManager = () => mount(HopGroupManager, {
  global: { stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' } } },
})
let wrapper: ReturnType<typeof mountManager>
beforeEach(() => {
  vi.clearAllMocks()
  list.mockResolvedValue([])
  get.mockResolvedValue(group)
  create.mockResolvedValue(group)
})
afterEach(() => wrapper?.unmount())

describe('load-balance first-hop groups', () => {
  it('offers load-balance with its connection-reuse hint and hides tolerance', async () => {
    wrapper = mountManager()
    await wrapper.get('button').trigger('click'); await flushPromises()
    const strategy = wrapper.findAll('select')[0]
    expect(strategy.findAll('option').map(option => option.element.value)).toEqual(['fallback', 'url-test', 'load-balance'])
    await strategy.setValue('load-balance')
    expect(wrapper.text()).toContain('HTTP keep-alive and HTTP/2 reuse one connection')
    expect(wrapper.text()).toContain('allow every first-hop node')
    expect(wrapper.text()).not.toContain('Latency tolerance')
    await wrapper.get('input[maxlength="255"]').setValue('lb')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(create.mock.calls[0][0].strategy).toBe('load-balance')
  })

  it('shows round-robin instead of a selected node and lists active connections', async () => {
    list.mockResolvedValue([group])
    wrapper = mountManager()
    await wrapper.get('button').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('Round-robin across healthy nodes')
    expect(wrapper.text()).not.toContain('No healthy node selected')
    expect(wrapper.text()).toContain('Active connections')
    expect(wrapper.find('tbody').text()).toContain('4')
  })
})
