import type { Account } from '@/types'

export const DEFAULT_OLLAMA_BASE_URL = 'https://ollama.com/v1'

export function isValidOllamaBaseUrl(raw: string | null | undefined): boolean {
  const trimmed = (raw ?? '').trim()
  if (!trimmed) return true
  if (trimmed.includes('?') || trimmed.includes('#')) return false
  try {
    const parsed = new URL(trimmed)
    if (parsed.protocol.toLowerCase() !== 'https:') return false
    if (parsed.hostname.toLowerCase() !== 'ollama.com') return false
    if (parsed.port !== '' && parsed.port !== '443') return false
    if (parsed.username !== '' || parsed.password !== '') return false
    return (
      parsed.pathname === '' ||
      parsed.pathname === '/' ||
      parsed.pathname === '/v1' ||
      parsed.pathname === '/v1/'
    )
  } catch {
    return false
  }
}

/** Keep rounded upstream refill hints relative instead of inventing a date. */
export function formatOllamaResetText(
  text: string | undefined,
  locale: string,
  t: (key: string, params: Record<string, string>) => string
): string | undefined {
  if (!text) return undefined
  const match = /^Refills in (\d+(?:\.\d+)?) (seconds?|minutes?|hours?|days?|weeks?|months?|years?)\.?$/i.exec(text.trim())
  if (!match) return text
  const amount = Number(match[1])
  if (!Number.isFinite(amount)) return text
  const unit = match[2].toLowerCase().replace(/s$/, '') as Intl.RelativeTimeFormatUnit
  const relative = new Intl.RelativeTimeFormat(locale, { numeric: 'always' }).format(amount, unit)
  return t('admin.accounts.ollamaCloud.approximateReset', { time: relative })
}

/**
 * 判断账号是否为 Ollama Cloud 账号（与后端 service.IsOllamaCloudUsageAccount 一致：platform === 'ollama' && type === 'apikey'）。
 */
export function isOllamaCloudAccount(
  account: Pick<Account, 'platform' | 'type'> | null | undefined
): boolean {
  return account?.platform === 'ollama' && account?.type === 'apikey'
}
