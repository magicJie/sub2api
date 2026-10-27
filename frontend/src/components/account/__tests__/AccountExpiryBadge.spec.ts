import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AccountExpiryBadge from '../AccountExpiryBadge.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key)
    })
  }
})

const DAY = 24 * 60 * 60
const NOW = new Date('2026-10-27T12:00:00Z')
const nowSec = NOW.getTime() / 1000

describe('AccountExpiryBadge', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  const badge = (expiresAt: number | null, compact = false) =>
    mount(AccountExpiryBadge, { props: { expiresAt, compact } }).get('[data-testid="account-expiry-badge"]')

  it('8 天：普通灰色', () => {
    const el = badge(nowSec + 8 * DAY)
    expect(el.attributes('data-level')).toBe('ok')
    expect(el.classes()).toContain('text-gray-500')
    expect(el.text()).toContain('admin.accounts.expiryBadge.daysLeft:{"days":8}')
  })

  it('7 天：琥珀色高亮', () => {
    const el = badge(nowSec + 7 * DAY)
    expect(el.attributes('data-level')).toBe('soon')
    expect(el.classes()).toContain('text-amber-600')
  })

  it('0 天（同日到期）：标红', () => {
    const el = badge(nowSec)
    expect(el.attributes('data-level')).toBe('expired')
    expect(el.classes()).toContain('text-red-600')
    expect(el.text()).toContain('admin.accounts.expiryBadge.expiredToday')
  })

  it('负数（已过期 2 天）：标红并显示过期天数', () => {
    const el = badge(nowSec - 2 * DAY)
    expect(el.classes()).toContain('text-red-600')
    expect(el.text()).toContain('admin.accounts.expiryBadge.expiredDaysAgo:{"days":2}')
  })

  it('未设置：弱提示', () => {
    const el = badge(null)
    expect(el.attributes('data-level')).toBe('unset')
    expect(el.classes()).toContain('text-gray-400')
    expect(el.text()).toBe('admin.accounts.expiryBadge.unset')
  })

  it('compact 只显示剩余天数', () => {
    const el = badge(nowSec + 3 * DAY, true)
    expect(el.text()).not.toContain('expiresOn')
    expect(el.text()).toContain('daysLeft')
  })
})
