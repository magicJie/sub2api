import type { AdminDataAccount, AdminDataPayload, AdminDataProxy } from '@/types'
import { getKiroDefaultModelMappings, sanitizeKiroModelMapping } from './models'

/**
 * kiro-account-manager（桌面端）“导出账号”生成的 JSON 数组 → sub2api 数据导入格式。
 *
 * 导出记录为 camelCase 的 Account（src-tauri/src/core/account.rs）：
 *   { id, email, label, status, accessToken, refreshToken, expiresAt, provider, authMethod,
 *     clientId, clientSecret, region, clientIdHash, startUrl, profileArn, machineId, proxyConfig, ... }
 * expiresAt 是导出机器本地时区的 "YYYY/MM/DD HH:mm:ss"（旧数据也可能是 RFC3339）。
 */
export interface KiroAccountManagerRecord {
  id?: string
  email?: string | null
  label?: string | null
  status?: string | null
  accessToken?: string | null
  refreshToken?: string | null
  expiresAt?: string | null
  provider?: string | null
  authMethod?: string | null
  clientId?: string | null
  clientSecret?: string | null
  clientIdHash?: string | null
  region?: string | null
  startUrl?: string | null
  profileArn?: string | null
  machineId?: string | null
  enabled?: boolean
  proxyConfig?: {
    enabled?: boolean
    protocol?: string
    host?: string
    port?: number
    username?: string | null
    password?: string | null
  } | null
}

export interface KiroAccountManagerSkip {
  name: string
  reason: 'missing_token' | 'unusable_status' | 'unsupported_auth' | 'duplicate'
}

export interface KiroAccountManagerConversion {
  payload: AdminDataPayload
  skipped: KiroAccountManagerSkip[]
}

// 与 CreateAccountModal 新建账号的默认值一致。
const DEFAULT_CONCURRENCY = 10
const DEFAULT_PRIORITY = 1
// 已封禁或 refresh token 失效的账号导入后也无法使用。
const UNUSABLE_STATUSES = new Set(['banned', 'invalid'])

const str = (v: unknown): string => (typeof v === 'string' ? v.trim() : '')

/** 识别 kiro-account-manager 导出：非空数组，且元素带 accessToken/refreshToken。 */
export function isKiroAccountManagerExport(data: unknown): data is KiroAccountManagerRecord[] {
  return (
    Array.isArray(data) &&
    data.length > 0 &&
    data.every(
      (item) =>
        item && typeof item === 'object' && !Array.isArray(item) && ('accessToken' in item || 'refreshToken' in item)
    )
  )
}

/** "YYYY/MM/DD HH:mm:ss"（本地时区）或 RFC3339 → RFC3339 UTC；无法解析返回空串。 */
export function normalizeKiroExpiresAt(raw: unknown): string {
  const value = str(raw)
  if (!value) return ''
  const local = /^(\d{4})\/(\d{1,2})\/(\d{1,2}) (\d{1,2}):(\d{2}):(\d{2})$/.exec(value)
  const date = local
    ? new Date(+local[1], +local[2] - 1, +local[3], +local[4], +local[5], +local[6])
    : /^\d{4}-\d{2}-\d{2}T/.test(value)
      ? new Date(value)
      : null
  if (!date || Number.isNaN(date.getTime())) return ''
  return date.toISOString().replace(/\.\d{3}Z$/, 'Z')
}

// 与后端 buildProxyKey 一致。
const proxyKey = (p: Pick<AdminDataProxy, 'protocol' | 'host' | 'port' | 'username' | 'password'>) =>
  [p.protocol, p.host, p.port, p.username ?? '', p.password ?? ''].join('|')

function defaultModelMapping(): Record<string, unknown> {
  return sanitizeKiroModelMapping(Object.fromEntries(getKiroDefaultModelMappings().map(({ from, to }) => [from, to])))
}

export function convertKiroAccountManagerExport(records: KiroAccountManagerRecord[]): KiroAccountManagerConversion {
  const accounts: AdminDataAccount[] = []
  const proxies = new Map<string, AdminDataProxy>()
  const skipped: KiroAccountManagerSkip[] = []
  const seenRefresh = new Set<string>()
  // kiro-account-manager 的默认 label 是 "Kiro Github 账号" 这类通用名，批量导出时会重名；重名时改用邮箱。
  const labelCount = new Map<string, number>()
  for (const r of records) {
    const label = str(r.label)
    if (label) labelCount.set(label, (labelCount.get(label) ?? 0) + 1)
  }
  const importedAt = new Date().toISOString().replace(/\.\d{3}Z$/, 'Z')

  records.forEach((record, index) => {
    const label = str(record.label)
    const fallback = `kiro-${str(record.id).slice(0, 8) || index + 1}`
    const name = label && labelCount.get(label) === 1 ? label : str(record.email) || (label ? `${label}-${index + 1}` : fallback)
    const accessToken = str(record.accessToken)
    const refreshToken = str(record.refreshToken)
    if (!accessToken || !refreshToken) {
      skipped.push({ name, reason: 'missing_token' })
      return
    }
    if (UNUSABLE_STATUSES.has(str(record.status).toLowerCase())) {
      skipped.push({ name, reason: 'unusable_status' })
      return
    }
    // sub2api 只实现了 social 与 IdC 刷新；external_idp（微软 token 端点）导入后无法续期。
    const rawAuth = str(record.authMethod).toLowerCase()
    // 缺省时与 kiro-account-manager 导出修复逻辑一致：有 clientId + clientSecret 即 IdC。
    const inferred = str(record.clientId) && str(record.clientSecret) ? 'idc' : 'social'
    const authMethod = rawAuth === 'idc' || rawAuth === 'social' ? rawAuth : rawAuth === '' ? inferred : ''
    if (!authMethod) {
      skipped.push({ name, reason: 'unsupported_auth' })
      return
    }
    if (seenRefresh.has(refreshToken)) {
      skipped.push({ name, reason: 'duplicate' })
      return
    }
    seenRefresh.add(refreshToken)

    const credentials: Record<string, unknown> = {
      access_token: accessToken,
      refresh_token: refreshToken,
      auth_method: authMethod,
      model_mapping: defaultModelMapping()
    }
    const optional: Array<[string, unknown]> = [
      ['profile_arn', record.profileArn],
      // 导出里 expiresAt 常为 null；缺失时记为导入时刻，让定时刷新立即续期并写入真实过期时间
      // （后端 KiroTokenRefresher 对没有 expires_at 的账号永不刷新）。
      ['expires_at', normalizeKiroExpiresAt(record.expiresAt) || importedAt],
      ['provider', record.provider],
      ['email', record.email],
      ['region', record.region],
      ['start_url', record.startUrl],
      ['machine_id', record.machineId],
      ...(authMethod === 'idc'
        ? ([
            ['client_id', record.clientId],
            ['client_secret', record.clientSecret],
            ['client_id_hash', record.clientIdHash]
          ] as Array<[string, unknown]>)
        : [])
    ]
    for (const [key, value] of optional) {
      const v = str(value)
      if (v) credentials[key] = v
    }

    let accountProxyKey: string | null = null
    const pc = record.proxyConfig
    if (pc?.enabled && str(pc.host) && Number(pc.port) > 0) {
      const proxy: AdminDataProxy = {
        proxy_key: '',
        name: `kam-${str(pc.host)}:${pc.port}`,
        protocol: str(pc.protocol).toLowerCase() === 'socks5' ? 'socks5' : 'http',
        host: str(pc.host),
        port: Number(pc.port),
        username: str(pc.username) || null,
        password: str(pc.password) || null,
        status: 'active'
      }
      proxy.proxy_key = proxyKey(proxy)
      if (!proxies.has(proxy.proxy_key)) proxies.set(proxy.proxy_key, proxy)
      accountProxyKey = proxy.proxy_key
    }

    accounts.push({
      name,
      notes: 'kiro-account-manager',
      platform: 'kiro',
      type: 'oauth',
      credentials,
      extra: {},
      proxy_key: accountProxyKey,
      concurrency: DEFAULT_CONCURRENCY,
      priority: DEFAULT_PRIORITY
    })
  })

  return {
    payload: {
      type: 'sub2api-data',
      version: 1,
      exported_at: new Date().toISOString(),
      proxies: [...proxies.values()],
      accounts
    },
    skipped
  }
}
