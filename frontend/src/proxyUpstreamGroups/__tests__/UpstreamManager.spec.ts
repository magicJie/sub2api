import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import UpstreamManager from '../UpstreamManager.vue'

const { listGroups, createGroup, updateGroup, removeGroup, listEndpoints, createEndpoint, updateEndpoint, removeEndpoint, showError } = vi.hoisted(() => ({
  listGroups: vi.fn(), createGroup: vi.fn(), updateGroup: vi.fn(), removeGroup: vi.fn(),
  listEndpoints: vi.fn(), createEndpoint: vi.fn(), updateEndpoint: vi.fn(), removeEndpoint: vi.fn(),
  showError: vi.fn(),
}))
vi.mock('../api', () => ({
  upstreamGroupsAPI: { list: listGroups, get: vi.fn(), create: createGroup, update: updateGroup, remove: removeGroup },
  upstreamEndpointsAPI: { list: listEndpoints, get: vi.fn(), create: createEndpoint, update: updateEndpoint, remove: removeEndpoint },
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key), locale: { value: 'en' } }),
}))

const health = { state: 'healthy', consecutive_failures: 0, consecutive_successes: 3, latency_ms: 42 }
const endpoints = [
  { id: 1, name: 'aio-a', protocol: 'socks5', host: '10.0.0.1', port: 7891, username: '', has_password: true, status: 'active', created_at: '', updated_at: '', health, group_names: ['g1'] },
  { id: 2, name: 'aio-b', protocol: 'http', host: '10.0.0.2', port: 7892, username: 'u', has_password: false, status: 'active', created_at: '', updated_at: '', health: { ...health, state: 'unhealthy', consecutive_failures: 4, last_error: 'timeout' }, group_names: [] },
]
const group = {
  id: 5, name: 'g1', status: 'active', probe_url: 'https://www.gstatic.com/generate_204',
  probe_interval_seconds: 30, fail_threshold: 3, recover_threshold: 2, failback_seconds: 300,
  created_at: '', updated_at: '', proxy_count: 2,
  slots: [
    { id: 11, name: 'slot-b', position: 1, weight: 3, endpoint_ids: [2], runtime: { available: false, current_endpoint_id: null } },
    { id: 10, name: 'slot-a', position: 0, weight: 1, endpoint_ids: [2, 1], runtime: { available: true, current_endpoint_id: 1 } },
  ],
}
const mountManager = () => mount(UpstreamManager, {
  global: { stubs: {
    BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
    ConfirmDialog: { props: ['show'], emits: ['confirm', 'cancel'], template: '<button v-if="show" data-test="confirm" @click="$emit(\'confirm\')" />' },
  } },
})
let wrapper: ReturnType<typeof mountManager>
async function open() {
  wrapper = mountManager()
  await wrapper.get('button').trigger('click'); await flushPromises()
}
const button = (text: string) => wrapper.findAll('button').find(b => b.text() === text)!
beforeEach(() => {
  vi.clearAllMocks()
  listGroups.mockResolvedValue([group])
  listEndpoints.mockResolvedValue(endpoints)
  createGroup.mockResolvedValue({ ...group, id: 6 })
  updateGroup.mockResolvedValue(group)
  createEndpoint.mockResolvedValue(endpoints[0])
  updateEndpoint.mockResolvedValue(endpoints[0])
})
afterEach(() => wrapper?.unmount())

describe('upstream group manager', () => {
  it('shows slots in position order with runtime availability and current endpoint', async () => {
    await open()
    const rows = wrapper.findAll('[data-test="slot-row"]')
    expect(rows.map(r => r.find('td').text().split(/\s/)[0])).toEqual(['slot-a', 'slot-b'])
    expect(rows[1].get('[data-test="slot-weight-share"]').text()).toContain('admin.proxyUpstream.slotWeightShare')
    expect(rows[0].text()).toContain('admin.proxyUpstream.available')
    expect(rows[0].get('[data-test="slot-current"]').text()).toBe('aio-a')
    expect(rows[1].text()).toContain('admin.proxyUpstream.unavailable')
    expect(rows[1].get('[data-test="slot-current"]').text()).toBe('—')
  })

  it('creates a group with ordered slots; a slot may have only a primary endpoint', async () => {
    listGroups.mockResolvedValue([])
    await open()
    await wrapper.get('[data-test="group-name"]').setValue('new')
    await wrapper.get('[data-test="slot-add"]').trigger('click')
    const adds = wrapper.findAll('[data-test="slot-add-endpoint"]')
    await adds[0].setValue('2')
    await adds[0].setValue('1')
    await adds[1].setValue('1')
    // 槽位 1 内把端点 1 提为主
    const firstSlot = wrapper.findAll('[data-test="slot-editor"]')[0]
    await firstSlot.findAll('[data-test="slot-endpoint"]')[1].find('button[aria-label="admin.proxyUpstream.moveUp"]').trigger('click')
    expect(firstSlot.findAll('[data-test="slot-endpoint"]')[0].text()).toContain('admin.proxyUpstream.primary')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(createGroup).toHaveBeenCalledTimes(1)
    const input = createGroup.mock.calls[0][0]
    expect(input).toMatchObject({ name: 'new', probe_interval_seconds: 30, fail_threshold: 3, recover_threshold: 2, failback_seconds: 300 })
    expect(input.slots.map((s: { endpoint_ids: number[] }) => s.endpoint_ids)).toEqual([[1, 2], [1]])
    expect(input.slots[0]).not.toHaveProperty('id')
    expect(input.slots.map((s: { weight: number }) => s.weight)).toEqual([1, 1])
  })

  it('sends edited slot weights and rejects out-of-range values', async () => {
    await open()
    await button('common.edit').trigger('click')
    const weights = wrapper.findAll('[data-test="slot-weight"]')
    await weights[0].setValue('0')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(updateGroup).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.proxyUpstream.slotWeightInvalid')
    await weights[0].setValue('5')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(updateGroup.mock.calls[0][1].slots.map((s: { weight: number }) => s.weight)).toEqual([5, 3])
  })

  it('refuses to save a slot without endpoints', async () => {
    listGroups.mockResolvedValue([])
    await open()
    await wrapper.get('[data-test="group-name"]').setValue('empty')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(createGroup).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.proxyUpstream.slotEndpointRequired')
  })

  it('keeps slot ids and reorders slots when editing', async () => {
    await open()
    await button('common.edit').trigger('click')
    const second = wrapper.findAll('[data-test="slot-editor"]')[1]
    await second.find('button[aria-label="admin.proxyUpstream.moveUp"]').trigger('click')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(updateGroup.mock.calls[0][0]).toBe(5)
    expect(updateGroup.mock.calls[0][1].slots).toEqual([
      { id: 11, name: 'slot-b', weight: 3, endpoint_ids: [2] },
      { id: 10, name: 'slot-a', weight: 1, endpoint_ids: [2, 1] },
    ])
  })

  it('maps the in-use conflict code to a localized message', async () => {
    removeGroup.mockRejectedValue({ status: 409, code: 409, reason: 'PROXY_UPSTREAM_GROUP_IN_USE', message: 'in use' })
    await open()
    await button('common.delete').trigger('click')
    await wrapper.get('[data-test="confirm"]').trigger('click'); await flushPromises()
    expect(showError).toHaveBeenCalledWith('admin.proxyUpstream.errors.PROXY_UPSTREAM_GROUP_IN_USE:{}')
  })
})

describe('upstream endpoint manager', () => {
  it('lists endpoint health and omits an empty password on update', async () => {
    await open()
    await button('admin.proxyUpstream.endpointsTab').trigger('click')
    const rows = wrapper.findAll('[data-test="endpoint-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('admin.proxyUpstream.healthy')
    expect(rows[0].text()).toContain('42 ms')
    expect(rows[1].text()).toContain('admin.proxyUpstream.unhealthy')
    expect(rows[1].text()).toContain('timeout')
    await rows[0].findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    expect(wrapper.get<HTMLInputElement>('[data-test="endpoint-password"]').element.placeholder).toBe('admin.proxyUpstream.passwordKeep')
    await wrapper.get('[data-test="endpoint-port"]').setValue('7900')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(updateEndpoint.mock.calls[0][0]).toBe(1)
    expect(updateEndpoint.mock.calls[0][1]).toEqual({ name: 'aio-a', protocol: 'socks5', host: '10.0.0.1', port: 7900, username: '', status: 'active' })
  })

  it('creates an endpoint with a password', async () => {
    await open()
    await button('admin.proxyUpstream.endpointsTab').trigger('click')
    await button('admin.proxyUpstream.createEndpoint').trigger('click')
    await wrapper.get('[data-test="endpoint-name"]').setValue('aio-c')
    await wrapper.get('[data-test="endpoint-protocol"]').setValue('http')
    await wrapper.get('[data-test="endpoint-host"]').setValue(' 10.0.0.3 ')
    await wrapper.get('[data-test="endpoint-password"]').setValue('secret')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(createEndpoint.mock.calls[0][0]).toEqual({ name: 'aio-c', protocol: 'http', host: '10.0.0.3', port: 7890, username: '', password: 'secret', status: 'active' })
  })
})
