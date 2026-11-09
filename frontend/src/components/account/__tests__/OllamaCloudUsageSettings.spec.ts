import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OllamaCloudUsageSettings from '../OllamaCloudUsageSettings.vue'
import type { Account, OllamaCloudUsageState } from '@/types'

const api = vi.hoisted(() => ({
  getOllamaCloudUsage: vi.fn(),
  setOllamaCloudUsageAutoRefresh: vi.fn(),
  refreshOllamaCloudUsage: vi.fn()
}))
const notifications = vi.hoisted(() => ({ showSuccess: vi.fn(), showError: vi.fn() }))

vi.mock('@/api/admin', () => ({ adminAPI: { accounts: api } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => notifications }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        if (key === 'admin.accounts.ollamaCloud.errors.OLLAMA_CLOUD_USAGE_REFRESH_RATE_LIMITED') {
          return `retry in ${params?.retry_after_seconds} seconds`
        }
        if (key === 'admin.accounts.ollamaCloud.requests') return 'requests'
        if (key === 'admin.accounts.ollamaCloud.inputTokens') return 'In'
        if (key === 'admin.accounts.ollamaCloud.cachedInputTokens') return 'Cached'
        if (key === 'admin.accounts.ollamaCloud.outputTokens') return 'Out'
        if (key === 'admin.accounts.ollamaCloud.otherModels') return 'Others'
        return key
      }
    })
  }
})

const state = (overrides: Partial<OllamaCloudUsageState> = {}): OllamaCloudUsageState => ({
  account_id: 7,
  eligible: true,
  configured: false,
  auto_refresh_enabled: false,
  ...overrides
})

const detailedState = (balance = '$0'): OllamaCloudUsageState => state({
  configured: true,
  snapshot: {
    status: 'ok',
    fetched_at: '2026-07-22T12:00:00Z',
    last_attempt_at: '2026-07-22T12:00:00Z',
    next_refresh_at: '2026-07-22T13:00:00Z',
    data: {
      five_hour: { used_percent: 5.6, reset_at: '2026-07-23T03:00:00Z' },
      seven_day: { used_percent: 14.2, reset_at: '2026-07-29T00:00:00Z' },
      balance,
      usage: {
        range: '7d',
        scope: 'self',
        from: '2026-07-15T00:00:00Z',
        until: '2026-07-22T12:00:00Z',
        totals: {
          request_count: 14,
          groups: [
            { model: 'gpt-oss:120b-cloud', request_count: 14 }
          ]
        }
      }
    }
  }
})

const account = (usage: OllamaCloudUsageState = state()): Account => ({
  id: 7,
  name: 'ollama',
  platform: 'ollama',
  type: 'apikey',
  ollama_cloud_usage: usage,
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

describe('OllamaCloudUsageSettings', () => {
  beforeEach(() => {
    Object.values(api).forEach(mock => mock.mockReset())
    notifications.showSuccess.mockReset()
    notifications.showError.mockReset()
  })

  it('uses the existing account state without a duplicate GET and removes session controls', async () => {
    const wrapper = mount(OllamaCloudUsageSettings, { props: { account: account(detailedState()) } })
    await flushPromises()

    expect(api.getOllamaCloudUsage).not.toHaveBeenCalled()
    expect(wrapper.find('#ollama-cloud-session').exists()).toBe(false)
    expect(wrapper.find('[data-testid="ollama-cloud-session-save"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="ollama-cloud-session-delete"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('admin.accounts.ollamaCloud.encryptionKeyRequired')
  })

  it('hides refresh and auto-refresh controls when no valid API key is configured', async () => {
    const wrapper = mount(OllamaCloudUsageSettings, {
      props: { account: account(state({ configured: false })) }
    })
    await flushPromises()

    expect(wrapper.get('[data-testid="ollama-cloud-usage-not-configured"]').text()).toContain('admin.accounts.ollamaCloud.cellNotConfigured')
    expect(wrapper.find('[data-testid="ollama-cloud-refresh"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="ollama-cloud-auto-refresh"]').exists()).toBe(false)
  })

  it('updates the account-level automatic refresh switch through its dedicated endpoint', async () => {
    api.setOllamaCloudUsageAutoRefresh.mockResolvedValueOnce(state({ configured: true, auto_refresh_enabled: true }))
    const wrapper = mount(OllamaCloudUsageSettings, {
      props: { account: account(state({ configured: true })) }
    })
    await flushPromises()

    await wrapper.get('[data-testid="ollama-cloud-auto-refresh"]').trigger('click')
    await flushPromises()
    expect(api.setOllamaCloudUsageAutoRefresh).toHaveBeenCalledWith(7, true)
    expect(wrapper.emitted('updated')?.at(-1)?.[0]).toMatchObject({ configured: true, auto_refresh_enabled: true })
  })

  it('shows legacy quota windows and 7d usage without fabricating absent token/cost metrics, and refreshes on demand', async () => {
    api.refreshOllamaCloudUsage.mockResolvedValueOnce(detailedState('$18.50'))
    const wrapper = mount(OllamaCloudUsageSettings, {
      props: { account: account(detailedState('$0')) }
    })
    await flushPromises()

    const details = wrapper.get('[data-testid="ollama-cloud-usage-details"]')
    expect(details.text()).not.toContain('admin.accounts.ollamaCloud.plan')
    expect(details.text()).toContain('$0')
    expect(wrapper.get('[data-testid="ollama-cloud-usage-range"]').text()).toBe('7d (2026-07-15 00:00:00 UTC – 2026-07-22 12:00:00 UTC)')
    expect(wrapper.get('[data-testid="ollama-cloud-usage-totals"]').text()).toBe('14 requests')
    expect(wrapper.get('[data-testid="ollama-cloud-usage-models"]').text()).toBe('7d gpt-oss:120b-cloud: 14 requests')
    expect(details.text()).toContain('admin.accounts.ollamaCloud.ok')

    await wrapper.get('[data-testid="ollama-cloud-refresh"]').trigger('click')
    await flushPromises()

    expect(api.refreshOllamaCloudUsage).toHaveBeenCalledWith(7)
    expect(wrapper.get('[data-testid="ollama-cloud-usage-details"]').text()).toContain('$18.50')
    expect(notifications.showSuccess).toHaveBeenCalled()
  })

  it('renders monthly quota with fixed 7d usage totals, token/cost metrics, model groups, and remainder without mislabelling as monthly', () => {
    const monthly = detailedState('$41.60')
    monthly.snapshot!.data = {
      monthly: { used_percent: 8.4, reset_at: '2026-10-18T02:05:47Z' },
      balance: '$41.60',
      usage: {
        range: '7d',
        scope: 'self',
        from: '2026-10-11T00:00:00Z',
        until: '2026-10-18T02:05:47Z',
        totals: {
          request_count: 761,
          usage_usd: 1.25,
          input_tokens: 12000,
          cached_input_tokens: 3000,
          output_tokens: 4500,
          groups: [
            {
              model: 'deepseek-v4.1-flash',
              request_count: 749,
              usage_usd: 1.2,
              input_tokens: 11000,
              cached_input_tokens: 3000,
              output_tokens: 4200
            }
          ],
          remainder: {
            request_count: 12,
            usage_usd: 0.05,
            input_tokens: 1000,
            cached_input_tokens: 0,
            output_tokens: 300
          }
        }
      }
    }

    const wrapper = mount(OllamaCloudUsageSettings, { props: { account: account(monthly) } })
    const details = wrapper.get('[data-testid="ollama-cloud-usage-details"]')

    expect(details.text()).toContain('admin.accounts.ollamaCloud.monthly')
    expect(details.text()).not.toContain('admin.accounts.ollamaCloud.fiveHour')
    expect(details.text()).not.toContain('admin.accounts.ollamaCloud.sevenDay')
    expect(wrapper.get('[data-testid="ollama-cloud-usage-range"]').text()).toBe('7d (2026-10-11 00:00:00 UTC – 2026-10-18 02:05:47 UTC)')
    expect(wrapper.get('[data-testid="ollama-cloud-usage-totals"]').text()).toBe('761 requests · $1.25 · In 12000 / Cached 3000 / Out 4500')
    expect(wrapper.get('[data-testid="ollama-cloud-usage-models"]').text()).toBe(
      '7d deepseek-v4.1-flash: 749 requests · $1.2 · In 11000 / Cached 3000 / Out 4200, 7d Others: 12 requests · $0.05 · In 1000 / Cached 0 / Out 300'
    )
    expect(wrapper.get('[data-testid="ollama-cloud-usage-models"]').text()).not.toContain('mo deepseek-v4.1-flash')
  })

  it('shows the structured manual refresh limit from the edit settings', async () => {
    api.refreshOllamaCloudUsage.mockRejectedValueOnce({
      status: 429,
      reason: 'OLLAMA_CLOUD_USAGE_REFRESH_RATE_LIMITED',
      metadata: { retry_after_seconds: '18' }
    })
    const wrapper = mount(OllamaCloudUsageSettings, {
      props: { account: account(detailedState()) }
    })

    await wrapper.get('[data-testid="ollama-cloud-refresh"]').trigger('click')
    await flushPromises()

    expect(notifications.showError).toHaveBeenCalledWith('retry in 18 seconds')
  })

  it('reports 401/403/invalid_json/incomplete_usage refresh failures and marks preserved data stale', async () => {
    const suspended = detailedState('$10')
    suspended.snapshot!.status = 'failed'
    suspended.snapshot!.last_error = 'http_error'
    suspended.snapshot!.http_status = 403
    api.refreshOllamaCloudUsage.mockResolvedValueOnce(suspended)

    const wrapper = mount(OllamaCloudUsageSettings, { props: { account: account(detailedState('$10')) } })
    await wrapper.get('[data-testid="ollama-cloud-refresh"]').trigger('click')
    await flushPromises()

    expect(notifications.showSuccess).not.toHaveBeenCalled()
    expect(notifications.showError).toHaveBeenCalledWith('admin.accounts.ollamaCloud.errors.account_suspended (HTTP 403)')
    const errorBanner = wrapper.get('[data-testid="ollama-cloud-usage-error"]')
    expect(errorBanner.text()).toContain('admin.accounts.ollamaCloud.errors.account_suspended (HTTP 403)')
    expect(errorBanner.text()).toContain('admin.accounts.ollamaCloud.staleData')
    expect(wrapper.get('[data-testid="ollama-cloud-usage-details"]').text()).toContain('$10')
  })
})
