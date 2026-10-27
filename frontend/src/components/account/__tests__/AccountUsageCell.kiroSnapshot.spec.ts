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
  'admin.accounts.usageWindow.kiroDaysRemaining': '剩余 {days} 天',
  'admin.accounts.usageWindow.kiroUpdatedAgo': '更新于 {time}',
  'admin.accounts.usageWindow.kiroUsagePending': '排队刷新中'
}

vi.mock('@/utils/format', async () => {
  const actual = await vi.importActual<typeof import('@/utils/format')>('@/utils/format')
  return {
    ...actual,
    formatRelativeTime: (date: string | null | undefined) =>
      date ? `${Math.floor((Date.now() - Date.parse(date)) / 60000)}分钟前` : '从未'
  }
})

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

describe('AccountUsageCell Kiro 快照更新时间', () => {
  beforeEach(() => {
    getUsage.mockReset()
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-10-27T08:00:00Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('显示"更新于 N 分钟前"', async () => {
    const wrapper = await mountKiro({
      updated_at: '2026-10-27T07:48:00Z',
      kiro_credit: { current_usage: 100, usage_limit: 5000, percentage_used: 2 }
    })
    expect(wrapper.get('[data-test="kiro-usage-updated"]').text()).toBe('更新于 12分钟前')
  })

  it('没有快照时显示排队刷新中', async () => {
    const wrapper = await mountKiro({ kiro_usage_pending: true })
    expect(wrapper.get('[data-test="kiro-usage-updated"]').text()).toBe('排队刷新中')
  })

  it('"全部刷新"排队中时同时显示更新时间与排队状态', async () => {
    const wrapper = await mountKiro({
      updated_at: '2026-10-27T07:55:00Z',
      kiro_usage_pending: true,
      kiro_credit: { current_usage: 100, usage_limit: 5000, percentage_used: 2 }
    })
    expect(wrapper.get('[data-test="kiro-usage-updated"]').text()).toBe('更新于 5分钟前 · 排队刷新中')
  })
})
