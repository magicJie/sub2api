import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import ProxyIncidentPanel from '../components/ProxyIncidentPanel.vue'
import type { ProxyEgressIncident } from '../api'

const {
  listIncidents,
  getEgressHealthConfig,
  updateEgressHealthConfig,
  migrateProxyAccounts,
  checkProxyHealth,
  resumeAccountProxy,
  showError,
  showSuccess
} = vi.hoisted(() => ({
  listIncidents: vi.fn(),
  getEgressHealthConfig: vi.fn(),
  updateEgressHealthConfig: vi.fn(),
  migrateProxyAccounts: vi.fn(),
  checkProxyHealth: vi.fn(),
  resumeAccountProxy: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('../api', () => ({
  default: {
    listIncidents,
    getEgressHealthConfig,
    updateEgressHealthConfig,
    migrateProxyAccounts,
    checkProxyHealth,
    resumeAccountProxy,
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess })
}))

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      params ? `${key}:${JSON.stringify(params)}` : key,
    locale: { value: 'en' }
  })
}))

function makeIncident(overrides: Partial<ProxyEgressIncident> = {}): ProxyEgressIncident {
  return {
    id: 1,
    proxy_id: 10,
    proxy_name: 'synthetic-egress-1',
    layer: 'egress',
    status: 'open',
    last_error: 'dial tcp timeout',
    fail_count: 3,
    first_failed_at: '2026-11-06T10:00:00Z',
    last_checked_at: '2026-11-06T10:05:00Z',
    resolved_at: null,
    handled_at: '2026-11-06T10:01:00Z',
    accounts: [
      {
        account_id: 101,
        account_name: 'synthetic-acct-ok',
        platform: 'openai',
        action: 'paused',
        to_proxy_id: null,
        message: '',
        created_at: '2026-11-06T10:00:00Z',
        resumed_at: null,
        schedulable: false,
        current_proxy_id: 10
      },
      {
        account_id: 102,
        account_name: 'synthetic-acct-capacity',
        platform: 'opencode',
        action: 'paused',
        to_proxy_id: null,
        message: '',
        created_at: '2026-11-06T10:00:00Z',
        resumed_at: null,
        schedulable: false,
        current_proxy_id: 10
      },
      {
        account_id: 103,
        account_name: 'synthetic-acct-legacy',
        platform: 'ollama',
        action: 'paused',
        to_proxy_id: null,
        message: '',
        created_at: '2026-11-06T10:00:00Z',
        resumed_at: null,
        schedulable: false,
        current_proxy_id: 10
      }
    ],
    ...overrides
  }
}

enableAutoUnmount(afterEach)

const panelStubs = {
  ReplaceProxyDialog: true,
  ConfirmDialog: {
    props: ['show'],
    emits: ['confirm', 'cancel'],
    template: '<button v-if="show" data-test="confirm-migrate" @click="$emit(\'confirm\')" />'
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  getEgressHealthConfig.mockResolvedValue({ enabled: true, interval_seconds: 120, fail_threshold: 3 })
})

describe('ProxyIncidentPanel batch migration diagnostics', () => {
  it('shows persistent per-account failure reasons for mixed batch migration and omits succeeded accounts', async () => {
    const incident = makeIncident()
    listIncidents.mockResolvedValue([incident])
    migrateProxyAccounts.mockResolvedValue([
      { account_id: 101, to_proxy_id: 20 },
      {
        account_id: 102,
        to_proxy_id: null,
        error: 'PROXY_POOL_NO_AVAILABLE_PROXY',
        error_message:
          'No candidate proxy within active capacity limit for platform opencode (proxy #20 at 2/2 active)'
      },
      {
        account_id: 103,
        to_proxy_id: null,
        error: 'PROXY_POOL_NO_AVAILABLE_PROXY'
      }
    ])

    const wrapper = mount(ProxyIncidentPanel, { global: { stubs: panelStubs } })
    await flushPromises()

    const migrateBtn = wrapper
      .findAll('button')
      .find((b) => b.text() === 'admin.proxyPools.migrateAll')
    expect(migrateBtn).toBeDefined()
    await migrateBtn!.trigger('click')
    await wrapper.get('[data-test="confirm-migrate"]').trigger('click')
    await flushPromises()

    expect(migrateProxyAccounts).toHaveBeenCalledWith(10)
    expect(showError).toHaveBeenCalledWith('admin.proxyPools.migrated:{"ok":1,"failed":2}')

    const failureBox = wrapper.get('[data-test="incident-migrate-failures"]')
    const items = failureBox.findAll('[data-test="incident-migrate-failure-item"]')
    expect(items).toHaveLength(2)

    expect(items[0].text()).toContain('synthetic-acct-capacity')
    expect(items[0].text()).toContain('#102')
    expect(items[0].text()).toContain(
      'No candidate proxy within active capacity limit for platform opencode (proxy #20 at 2/2 active)'
    )

    expect(items[1].text()).toContain('synthetic-acct-legacy')
    expect(items[1].text()).toContain('#103')
    expect(items[1].text()).toContain('admin.proxyPools.errors.PROXY_POOL_NO_AVAILABLE_PROXY:{}')

    expect(failureBox.text()).not.toContain('synthetic-acct-ok')
    expect(failureBox.text()).not.toContain('#101')
  })

  it('scopes failures per incident and clears prior attempt failures when a new migration attempt starts', async () => {
    const incidentA = makeIncident({ id: 1, proxy_id: 10, proxy_name: 'synthetic-egress-1' })
    const incidentB = makeIncident({
      id: 2,
      proxy_id: 11,
      proxy_name: 'synthetic-egress-2',
      accounts: [
        {
          account_id: 201,
          account_name: 'synthetic-other-incident',
          platform: 'openai',
          action: 'paused',
          to_proxy_id: null,
          message: '',
          created_at: '2026-11-06T10:00:00Z',
          resumed_at: null,
          schedulable: false,
          current_proxy_id: 11
        }
      ]
    })
    listIncidents.mockResolvedValue([incidentA, incidentB])
    migrateProxyAccounts
      .mockResolvedValueOnce([
        {
          account_id: 102,
          to_proxy_id: null,
          error: 'PROXY_IP_POLICY_MISSING',
          error_message: 'platform opencode has no IP policy configured'
        }
      ])
      .mockResolvedValueOnce([{ account_id: 102, to_proxy_id: 20 }])

    const wrapper = mount(ProxyIncidentPanel, { global: { stubs: panelStubs } })
    await flushPromises()

    await wrapper
      .findAll('button')
      .filter((b) => b.text() === 'admin.proxyPools.migrateAll')[0]
      .trigger('click')
    await wrapper.get('[data-test="confirm-migrate"]').trigger('click')
    await flushPromises()

    expect(wrapper.findAll('[data-test="incident-migrate-failures"]')).toHaveLength(1)
    expect(wrapper.get('[data-test="incident-migrate-failures"]').text()).toContain('#102')
    expect(wrapper.get('[data-test="incident-migrate-failures"]').text()).not.toContain('#201')

    await wrapper
      .findAll('button')
      .filter((b) => b.text() === 'admin.proxyPools.migrateAll')[0]
      .trigger('click')
    await wrapper.get('[data-test="confirm-migrate"]').trigger('click')
    await flushPromises()

    expect(wrapper.findAll('[data-test="incident-migrate-failures"]')).toHaveLength(0)
    expect(showSuccess).toHaveBeenCalledWith('admin.proxyPools.migrated:{"ok":1,"failed":0}')
  })
})
