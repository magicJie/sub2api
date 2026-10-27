import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountUsageCell from '../AccountUsageCell.vue'
import type { Account, AccountUsageInfo } from '@/types'

const { getUsage } = vi.hoisted(() => ({
  getUsage: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getUsage
    }
  }
}))

// 与 zh 文案一致的插值，便于断言实际展示文字。
const messages: Record<string, string> = {
  'admin.accounts.usageWindow.kiroRemaining': '剩 {remaining} / {limit}',
  'admin.accounts.usageWindow.kiroNearlyExhausted': '即将耗尽',
  'admin.accounts.usageWindow.kiroUpstreamRejected': '已被上游拒绝，{time} 恢复',
  'admin.accounts.usageWindow.kiroDaysRemaining': '剩余 {days} 天'
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        (messages[key] ?? key).replace(/\{(\w+)\}/g, (_, name) => String(params?.[name] ?? ''))
    })
  }
})

function makeKiroAccount(overrides: Partial<Account> = {}): Account {
  return {
    id: 76,
    name: 'kiro',
    platform: 'kiro',
    type: 'oauth',
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    status: 'active',
    error_message: null,
    last_used_at: null,
    expires_at: null,
    auto_pause_on_expired: true,
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z',
    schedulable: true,
    rate_limited_at: null,
    rate_limit_reset_at: null,
    overload_until: null,
    temp_unschedulable_until: null,
    temp_unschedulable_reason: null,
    session_window_start: null,
    session_window_end: null,
    session_window_status: null,
    ...overrides
  }
}

async function mountKiro(usage: Partial<AccountUsageInfo>, account: Partial<Account> = {}) {
  getUsage.mockResolvedValue(usage)
  const wrapper = mount(AccountUsageCell, {
    props: { account: makeKiroAccount(account) },
    global: {
      stubs: {
        UsageProgressBar: true,
        AccountQuotaInfo: true
      }
    }
  })
  await flushPromises()
  return wrapper
}

describe('AccountUsageCell Kiro 额度', () => {
  beforeEach(() => {
    getUsage.mockReset()
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-10-27T08:00:00Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('显示剩余量，保留一位小数且不用 K 缩写', async () => {
    const wrapper = await mountKiro({
      kiro_credit: { current_usage: 1234.56, usage_limit: 5000, percentage_used: 24.69 },
      kiro_bonus: { current_usage: 120.25, usage_limit: 500, percentage_used: 24.05, days_remaining: 7 }
    })

    const credit = wrapper.get('[data-test="kiro-credit-remaining"]')
    expect(credit.text()).toBe('剩 3765.4 / 5000')
    expect(credit.classes()).toContain('text-gray-500')
    expect(wrapper.get('[data-test="kiro-bonus-remaining"]').text()).toBe('剩 379.7 / 500 (剩余 7 天)')
    expect(wrapper.find('[data-test="kiro-upstream-rejected"]').exists()).toBe(false)
  })

  it('剩余 < 50 credit 或 < 1% 时标记即将耗尽', async () => {
    // 生产 76 号：4988.28 / 5000
    const wrapper = await mountKiro({
      kiro_credit: { current_usage: 4988.28, usage_limit: 5000, percentage_used: 99.77 }
    })

    const credit = wrapper.get('[data-test="kiro-credit-remaining"]')
    expect(credit.text()).toBe('剩 11.7 / 5000 · 即将耗尽')
    expect(credit.classes()).toContain('text-amber-600')
    expect(wrapper.find('[data-test="kiro-upstream-rejected"]').exists()).toBe(false)

    // 大额度下剩余不足 1%（但 > 50）同样提示
    const large = await mountKiro({
      kiro_credit: { current_usage: 9930, usage_limit: 10000, percentage_used: 99.3 }
    })
    expect(large.get('[data-test="kiro-credit-remaining"]').text()).toBe('剩 70 / 10000 · 即将耗尽')

    const healthy = await mountKiro({
      kiro_credit: { current_usage: 4900, usage_limit: 5000, percentage_used: 98 }
    })
    expect(healthy.get('[data-test="kiro-credit-remaining"]').text()).toBe('剩 100 / 5000')
  })

  it('账号限流中时显示已被上游拒绝和 reset 时间，并换颜色', async () => {
    const wrapper = await mountKiro(
      { kiro_credit: { current_usage: 5000, usage_limit: 5000, percentage_used: 100 } },
      { rate_limited_at: '2026-10-27T04:02:00Z', rate_limit_reset_at: '2026-11-01T00:00:00Z' }
    )

    const credit = wrapper.get('[data-test="kiro-credit-remaining"]')
    expect(credit.text()).toBe('剩 0 / 5000')
    expect(credit.classes()).toContain('text-rose-600')

    const rejected = wrapper.get('[data-test="kiro-upstream-rejected"]')
    expect(rejected.text()).toMatch(/^已被上游拒绝，.*2026.* 恢复$/)
    expect(rejected.classes()).toContain('text-rose-600')
  })

  it('限流时间已过时不再显示已被上游拒绝', async () => {
    const wrapper = await mountKiro(
      { kiro_credit: { current_usage: 4988.28, usage_limit: 5000, percentage_used: 99.77 } },
      { rate_limit_reset_at: '2026-10-01T00:00:00Z' }
    )

    expect(wrapper.find('[data-test="kiro-upstream-rejected"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="kiro-credit-remaining"]').classes()).toContain('text-amber-600')
  })
})
