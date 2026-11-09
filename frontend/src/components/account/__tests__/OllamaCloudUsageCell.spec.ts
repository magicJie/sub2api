import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OllamaCloudUsageCell from '../OllamaCloudUsageCell.vue'
import UsageProgressBar from '../UsageProgressBar.vue'
import type { Account, OllamaCloudUsageState } from '@/types'

const { refreshOllamaCloudUsage } = vi.hoisted(() => ({
  refreshOllamaCloudUsage: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      refreshOllamaCloudUsage
    }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const usageState = (): OllamaCloudUsageState => ({
  account_id: 7,
  eligible: true,
  configured: true,
  auto_refresh_enabled: false,
  snapshot: {
    status: 'ok',
    fetched_at: '2026-07-22T12:00:00Z',
    last_attempt_at: '2026-07-22T12:00:00Z',
    next_refresh_at: '2026-07-22T13:00:00Z',
    data: {
      five_hour: { used_percent: 5.6, reset_at: '2026-07-23T03:00:00Z' },
      seven_day: { used_percent: 14.2, reset_at: '2026-07-29T00:00:00Z' },
      balance: '$0',
      usage: {
        range: '7d',
        scope: 'self',
        from: '2026-07-15T00:00:00Z',
        until: '2026-07-22T12:00:00Z',
        totals: {
          request_count: 14,
          groups: [{ model: 'gpt-oss:120b-cloud', request_count: 14 }]
        }
      }
    }
  }
})

const account = (state = usageState()): Account => ({
  id: 7,
  name: 'ollama',
  platform: 'ollama',
  type: 'apikey',
  ollama_cloud_usage: state,
  proxy_id: null,
  concurrency: 1,
  priority: 1,
  status: 'active',
  error_message: null,
  last_used_at: null,
  expires_at: null,
  auto_pause_on_expired: false,
  created_at: '2026-07-22T00:00:00Z',
  updated_at: '2026-07-22T00:00:00Z',
  schedulable: true,
  rate_limited_at: null,
  rate_limit_reset_at: null,
  overload_until: null,
  temp_unschedulable_until: null,
  temp_unschedulable_reason: null,
  session_window_start: null,
  session_window_end: null,
  session_window_status: null
})

describe('OllamaCloudUsageCell', () => {
  beforeEach(() => {
    refreshOllamaCloudUsage.mockReset()
  })

  it('shows the subscription expiry badge only when an expiry is set', () => {
    const unset = mount(OllamaCloudUsageCell, { props: { account: account() } })
    expect(unset.find('[data-testid="account-expiry-badge"]').exists()).toBe(false)

    const expiring = mount(OllamaCloudUsageCell, {
      props: { account: { ...account(), expires_at: Math.floor(Date.now() / 1000) + 2 * 86400 } }
    })
    expect(expiring.get('[data-testid="account-expiry-badge"]').attributes('data-level')).toBe('soon')
  })

  it('renders native 5h and 7d windows with the configured-account query action', () => {
    const wrapper = mount(OllamaCloudUsageCell, { props: { account: account() } })
    const cell = wrapper.get('[data-testid="ollama-cloud-usage-cell"]')
    expect(cell.classes()).toEqual(expect.arrayContaining(['min-w-0', 'max-w-full']))
    expect(cell.classes()).not.toContain('min-w-[12rem]')

    const bars = wrapper.findAllComponents(UsageProgressBar)
    expect(bars).toHaveLength(2)
    expect(bars[0].props()).toMatchObject({
      label: '5h',
      utilization: 5.6,
      resetsAt: '2026-07-23T03:00:00Z'
    })
    expect(bars[1].props()).toMatchObject({
      label: '7d',
      utilization: 14.2,
      resetsAt: '2026-07-29T00:00:00Z'
    })

    expect(wrapper.find('[data-testid="ollama-cloud-usage-details"]').exists()).toBe(false)
    const query = wrapper.get('[data-testid="ollama-cloud-usage-query"]')
    expect(query.classes()).toEqual(expect.arrayContaining(['text-blue-600', 'hover:bg-blue-50']))
    expect(query.text()).toContain('admin.accounts.usageWindow.activeQuery')
    expect(wrapper.text()).not.toContain('max')
    expect(wrapper.get('[data-testid="ollama-cloud-balance"]').text()).toContain('$0')
    expect(wrapper.text()).not.toContain('gpt-oss:120b-cloud')
  })

  it('reacts to an account snapshot update', async () => {
    const wrapper = mount(OllamaCloudUsageCell, { props: { account: account() } })
    const next = usageState()
    next.snapshot!.data!.five_hour!.used_percent = 43

    await wrapper.setProps({ account: account(next) })

    expect(wrapper.findAllComponents(UsageProgressBar)[0].props('utilization')).toBe(43)
  })

  it('renders the current monthly Ollama usage window', () => {
    const monthly = usageState()
    monthly.snapshot!.data = {
      monthly: { used_percent: 8.4, reset_at: '2026-10-18T02:05:47Z' }
    }

    const wrapper = mount(OllamaCloudUsageCell, { props: { account: account(monthly) } })
    const bars = wrapper.findAllComponents(UsageProgressBar)

    expect(bars).toHaveLength(1)
    expect(bars[0].props()).toMatchObject({
      label: 'mo',
      utilization: 8.4,
      resetsAt: '2026-10-18T02:05:47Z'
    })
  })

  it('queries through the edit-page refresh endpoint and emits the updated state', async () => {
    const next = usageState()
    next.snapshot!.data!.five_hour!.used_percent = 43
    refreshOllamaCloudUsage.mockResolvedValueOnce(next)
    const wrapper = mount(OllamaCloudUsageCell, { props: { account: account() } })

    await wrapper.get('[data-testid="ollama-cloud-usage-query"]').trigger('click')
    await flushPromises()

    expect(refreshOllamaCloudUsage).toHaveBeenCalledWith(7)
    expect(wrapper.findAllComponents(UsageProgressBar)[0].props('utilization')).toBe(43)
    expect(wrapper.emitted<OllamaCloudUsageState[]>('updated')?.[0]?.[0]).toEqual(next)
  })

  it('hides the query action until a valid API key is configured', () => {
    const state = usageState()
    state.configured = false

    const wrapper = mount(OllamaCloudUsageCell, { props: { account: account(state) } })

    expect(wrapper.find('[data-testid="ollama-cloud-usage-query"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="ollama-cloud-usage-not-configured"]').text()).toContain('admin.accounts.ollamaCloud.cellNotConfigured')
  })

  it('accepts balance-only snapshots with zero allowance without false incomplete warnings', () => {
    const balanceOnly = usageState()
    balanceOnly.snapshot!.data = { balance: '$40' }
    const wrapper = mount(OllamaCloudUsageCell, { props: { account: account(balanceOnly) } })
    expect(wrapper.findAllComponents(UsageProgressBar)).toHaveLength(0)
    expect(wrapper.get('[data-testid="ollama-cloud-balance"]').text()).toContain('$40')
    expect(wrapper.find('[data-testid="ollama-cloud-usage-warning"]').exists()).toBe(false)
  })

  it('keeps previous data and marks it stale after a failed refresh', async () => {
    const failed = usageState()
    failed.snapshot!.status = 'failed'
    failed.snapshot!.last_error = 'incomplete_usage'
    refreshOllamaCloudUsage.mockResolvedValueOnce(failed)
    const wrapper = mount(OllamaCloudUsageCell, { props: { account: account() } })
    await wrapper.get('[data-testid="ollama-cloud-usage-query"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAllComponents(UsageProgressBar)).toHaveLength(2)
    const warning = wrapper.get('[data-testid="ollama-cloud-usage-warning"]')
    expect(warning.text()).toContain('admin.accounts.ollamaCloud.errors.incomplete_usage')
    expect(warning.text()).toContain('admin.accounts.ollamaCloud.staleData')
  })

  it('surfaces HTTP 403 suspended account and HTTP 401 unauthorized API key errors with stale indicator', () => {
    const suspended = usageState()
    suspended.snapshot!.status = 'failed'
    suspended.snapshot!.last_error = 'http_error'
    suspended.snapshot!.http_status = 403
    const suspendedWrapper = mount(OllamaCloudUsageCell, { props: { account: account(suspended) } })
    const suspendedWarning = suspendedWrapper.get('[data-testid="ollama-cloud-usage-warning"]')
    expect(suspendedWarning.text()).toContain('admin.accounts.ollamaCloud.errors.account_suspended')
    expect(suspendedWarning.text()).toContain('HTTP 403')
    expect(suspendedWarning.text()).toContain('admin.accounts.ollamaCloud.staleData')

    const unauthorized = usageState()
    unauthorized.snapshot!.status = 'unauthorized'
    unauthorized.snapshot!.last_error = 'unauthorized'
    unauthorized.snapshot!.http_status = 401
    const unauthorizedWrapper = mount(OllamaCloudUsageCell, { props: { account: account(unauthorized) } })
    const unauthorizedWarning = unauthorizedWrapper.get('[data-testid="ollama-cloud-usage-warning"]')
    expect(unauthorizedWarning.text()).toContain('admin.accounts.ollamaCloud.errors.unauthorized')
    expect(unauthorizedWarning.text()).toContain('HTTP 401')
  })
})
