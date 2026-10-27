/** Set an expiry from local calendar months, clamping month-end dates. */
export function getAccountExpiryTimestamp(months: number, now = new Date()): number {
  const expiry = new Date(now.getTime())
  const lastDay = new Date(expiry.getFullYear(), expiry.getMonth() + months + 1, 0).getDate()
  expiry.setMonth(expiry.getMonth() + months, Math.min(expiry.getDate(), lastDay))
  return Math.floor(expiry.getTime() / 1000)
}

const DAY_MS = 24 * 60 * 60 * 1000
/** 剩余天数不超过该值时高亮为"即将到期"。 */
export const ACCOUNT_EXPIRY_SOON_DAYS = 7

export type AccountExpiryLevel = 'unset' | 'ok' | 'soon' | 'expired'

export interface AccountExpiryStatus {
  level: AccountExpiryLevel
  /** 未到期时为剩余整天数（向下取整，不足 1 天为 0）；已到期时为已过去的整天数的相反数（同日到期为 0）。 */
  days: number | null
}

/** 按到期时间（unix 秒）计算剩余天数与高亮级别：到期时刻及之后算已到期。 */
export function getAccountExpiryStatus(expiresAt: number | null | undefined, now = new Date()): AccountExpiryStatus {
  if (!expiresAt) return { level: 'unset', days: null }
  const remainingMs = expiresAt * 1000 - now.getTime()
  if (remainingMs <= 0) {
    const ago = Math.floor(-remainingMs / DAY_MS)
    return { level: 'expired', days: ago === 0 ? 0 : -ago }
  }
  const days = Math.floor(remainingMs / DAY_MS)
  return { level: days <= ACCOUNT_EXPIRY_SOON_DAYS ? 'soon' : 'ok', days }
}

/** 续费顺延：从当前到期日往后加若干自然月；到期日为空或已过期时从现在算。 */
export function extendAccountExpiry(expiresAt: number | null | undefined, months: number, now = new Date()): number {
  const base = expiresAt && expiresAt * 1000 > now.getTime() ? new Date(expiresAt * 1000) : now
  return getAccountExpiryTimestamp(months, base)
}
