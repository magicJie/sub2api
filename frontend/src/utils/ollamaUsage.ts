import type { Account, OllamaCloudUsageSnapshot } from '@/types'

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

/**
 * 判断账号是否为 Ollama Cloud 账号（与后端 service.IsOllamaCloudUsageAccount 一致：platform === 'ollama' && type === 'apikey'）。
 */
export function isOllamaCloudAccount(
  account: Pick<Account, 'platform' | 'type'> | null | undefined
): boolean {
  return account?.platform === 'ollama' && account?.type === 'apikey'
}

export function formatOllamaSnapshotError(
  snapshot: Pick<OllamaCloudUsageSnapshot, 'status' | 'last_error' | 'http_status'> | null | undefined,
  t: (key: string, defaultMsg?: string) => string
): string {
  if (!snapshot) return ''
  if (snapshot.http_status === 403 && (snapshot.last_error === 'http_error' || snapshot.status === 'failed')) {
    const msg = t('admin.accounts.ollamaCloud.errors.account_suspended', 'Account suspended (HTTP 403)')
    return msg.includes('403') ? msg : `${msg} (HTTP 403)`
  }
  if (snapshot.last_error) {
    const msg = t(`admin.accounts.ollamaCloud.errors.${snapshot.last_error}`, snapshot.last_error)
    if (snapshot.http_status && !msg.includes(String(snapshot.http_status))) {
      return `${msg} (HTTP ${snapshot.http_status})`
    }
    return msg
  }
  if (snapshot.status === 'unauthorized') {
    return t('admin.accounts.ollamaCloud.unauthorized')
  }
  if (snapshot.status === 'failed') {
    return snapshot.http_status
      ? `${t('admin.accounts.ollamaCloud.failed')} (HTTP ${snapshot.http_status})`
      : t('admin.accounts.ollamaCloud.failed')
  }
  return ''
}
