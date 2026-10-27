import { describe, expect, it } from 'vitest'
import { extendAccountExpiry, getAccountExpiryStatus, getAccountExpiryTimestamp } from '../accountExpiry'

describe('getAccountExpiryTimestamp', () => {
  it.each([
    ['2026-01-31T12:34:56.789', 1, '2026-02-28T12:34:56'],
    ['2028-01-31T12:34:56.789', 1, '2028-02-29T12:34:56'],
    ['2028-02-29T12:34:56.789', 12, '2029-02-28T12:34:56'],
    ['2026-12-31T12:34:56.789', 1, '2027-01-31T12:34:56'],
    // With TZ=Australia/Melbourne, October 1 contains a DST gap; avoid using it as an intermediate date.
    ['2028-10-31T02:30:00', 1, '2028-11-30T02:30:00'],
  ])('adds %s by %i calendar months with month-end clamping', (input, months, expected) => {
    const now = new Date(input)
    const originalTimestamp = now.getTime()

    expect(getAccountExpiryTimestamp(months, now)).toBe(new Date(expected).getTime() / 1000)
    expect(now.getTime()).toBe(originalTimestamp)
  })
})

describe('getAccountExpiryStatus', () => {
  const now = new Date('2026-10-27T12:00:00Z')
  const sec = (ms: number) => Math.floor(ms / 1000)
  const day = 24 * 60 * 60 * 1000

  it.each([
    ['8 天', sec(now.getTime() + 8 * day), 'ok', 8],
    ['7 天', sec(now.getTime() + 7 * day), 'soon', 7],
    ['7 天多 1 小时（向下取整仍算 7 天）', sec(now.getTime() + 7 * day + 3600_000), 'soon', 7],
    ['不足 1 天', sec(now.getTime() + 3600_000), 'soon', 0],
    ['正好到期', sec(now.getTime()), 'expired', 0],
    ['过期不足 1 天', sec(now.getTime() - 3600_000), 'expired', 0],
    ['过期 3 天', sec(now.getTime() - 3 * day), 'expired', -3]
  ])('%s', (_label, expiresAt, level, days) => {
    expect(getAccountExpiryStatus(expiresAt, now)).toEqual({ level, days })
  })

  it.each([null, undefined, 0])('未设置（%s）', (value) => {
    expect(getAccountExpiryStatus(value, now)).toEqual({ level: 'unset', days: null })
  })
})

describe('extendAccountExpiry', () => {
  const ts = (iso: string) => new Date(iso).getTime() / 1000
  const now = new Date('2026-01-15T09:00:00')

  it.each([
    ['2026-01-31T10:00:00', 1, '2026-02-28T10:00:00'],
    ['2026-03-31T10:00:00', 1, '2026-04-30T10:00:00'],
    ['2026-02-20T10:00:00', 1, '2026-03-20T10:00:00'],
    ['2026-02-20T10:00:00', 12, '2027-02-20T10:00:00'],
    ['2028-02-29T10:00:00', 12, '2029-02-28T10:00:00']
  ])('从当前到期日 %s 顺延 %i 个月', (current, months, expected) => {
    expect(extendAccountExpiry(ts(current), months, now)).toBe(ts(expected))
  })

  it('到期日为空时从现在算', () => {
    expect(extendAccountExpiry(null, 1, now)).toBe(ts('2026-02-15T09:00:00'))
    expect(extendAccountExpiry(undefined, 12, now)).toBe(ts('2027-01-15T09:00:00'))
  })

  it('已过期时从现在算', () => {
    expect(extendAccountExpiry(ts('2025-12-01T00:00:00'), 1, now)).toBe(ts('2026-02-15T09:00:00'))
    expect(extendAccountExpiry(ts('2026-01-15T09:00:00'), 1, now)).toBe(ts('2026-02-15T09:00:00'))
  })
})
