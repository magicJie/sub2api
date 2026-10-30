import { describe, expect, it } from 'vitest'
import {
  convertKiroAccountManagerExport,
  isKiroAccountManagerExport,
  normalizeKiroExpiresAt
} from './accountManagerImport'

// 字段与 kiro-account-manager export_accounts 输出一致（camelCase Account）。
const social = {
  id: 'a1b2c3d4-0000', email: 'u@example.com', label: 'Kiro BuilderId 账号', status: 'active',
  addedAt: '2026/10/20 10:00:00', accessToken: 'at-1', refreshToken: 'rt-1', expiresAt: '2026/10/29 18:30:00',
  provider: 'Github', userId: 'uid', authMethod: 'social', clientId: null, clientSecret: null, region: null,
  profileArn: 'arn:aws:codewhisperer:us-east-1:123:profile/X', machineId: 'mid-1', usageData: { x: 1 },
  tagLinks: [], enabled: true, failureCount: 0, successCount: 0
}
const idc = {
  ...social, id: 'idc', label: '', email: 'idc@example.com', accessToken: 'at-2', refreshToken: 'rt-2',
  authMethod: 'IdC', provider: 'BuilderId', clientId: 'cid', clientSecret: 'csec', clientIdHash: 'h',
  region: 'us-east-1', startUrl: 'https://view.awsapps.com/start', expiresAt: '2026-10-29T10:30:00Z',
  proxyConfig: { enabled: true, protocol: 'socks5', host: '10.0.0.9', port: 1080, username: 'u', password: 'p' }
}

describe('kiro-account-manager import', () => {
  it('detects the export array but not sub2api data files', () => {
    expect(isKiroAccountManagerExport([social])).toBe(true)
    expect(isKiroAccountManagerExport({ proxies: [], accounts: [] })).toBe(false)
    expect(isKiroAccountManagerExport([])).toBe(false)
  })

  it('normalizes local and RFC3339 expiry to RFC3339 UTC', () => {
    const local = new Date(2026, 9, 29, 18, 30, 0).toISOString().replace(/\.\d{3}Z$/, 'Z')
    expect(normalizeKiroExpiresAt('2026/10/29 18:30:00')).toBe(local)
    expect(normalizeKiroExpiresAt('2026-10-29T10:30:00Z')).toBe('2026-10-29T10:30:00Z')
    expect(normalizeKiroExpiresAt('garbage')).toBe('')
    expect(normalizeKiroExpiresAt(null)).toBe('')
  })

  it('converts social and IdC records into kiro oauth accounts', () => {
    const { payload, skipped } = convertKiroAccountManagerExport([social, idc])
    expect(skipped).toEqual([])
    expect(payload).toMatchObject({ type: 'sub2api-data', version: 1 })
    const [a, b] = payload.accounts
    expect(a).toMatchObject({ name: 'Kiro BuilderId 账号', platform: 'kiro', type: 'oauth', proxy_key: null, concurrency: 10, priority: 1 })
    expect(a.credentials).toMatchObject({
      access_token: 'at-1', refresh_token: 'rt-1', auth_method: 'social', provider: 'Github',
      profile_arn: social.profileArn, machine_id: 'mid-1', email: 'u@example.com'
    })
    expect(a.credentials).not.toHaveProperty('client_id')
    expect(a.credentials).not.toHaveProperty('usage_data')
    expect(Object.keys(a.credentials.model_mapping as object).length).toBeGreaterThan(0)

    expect(b.name).toBe('idc@example.com')
    expect(b.credentials).toMatchObject({
      auth_method: 'idc', client_id: 'cid', client_secret: 'csec', client_id_hash: 'h',
      region: 'us-east-1', start_url: 'https://view.awsapps.com/start', expires_at: '2026-10-29T10:30:00Z'
    })
    expect(payload.proxies).toEqual([{
      proxy_key: 'socks5|10.0.0.9|1080|u|p', name: 'kam-10.0.0.9:1080', protocol: 'socks5',
      host: '10.0.0.9', port: 1080, username: 'u', password: 'p', status: 'active'
    }])
    expect(b.proxy_key).toBe('socks5|10.0.0.9|1080|u|p')
  })

  it('converts a whole exported array in one batch, sharing proxies', () => {
    const records = Array.from({ length: 50 }, (_, i) => ({
      ...social, id: `id-${i}`, label: `kiro-${i}`, accessToken: `at-${i}`, refreshToken: `rt-${i}`,
      proxyConfig: i % 2 ? { enabled: true, protocol: 'http', host: '10.0.0.1', port: 8080 } : null
    }))
    const { payload, skipped } = convertKiroAccountManagerExport(records)
    expect(skipped).toEqual([])
    expect(payload.accounts).toHaveLength(50)
    expect(payload.accounts.map((a) => a.name)).toEqual(records.map((r) => r.label))
    expect(payload.proxies).toHaveLength(1)
    expect(payload.accounts.filter((a) => a.proxy_key === payload.proxies[0].proxy_key)).toHaveLength(25)
  })

  it('renames duplicated default labels to email and fills missing expiry', () => {
    const records = [0, 1, 2].map((i) => ({
      ...social, label: 'Kiro Github 账号', email: i === 2 ? null : `u${i}@example.com`,
      accessToken: `at-${i}`, refreshToken: `rt-${i}`, expiresAt: null
    }))
    const before = Date.now()
    const { payload } = convertKiroAccountManagerExport(records)
    expect(payload.accounts.map((a) => a.name)).toEqual(['u0@example.com', 'u1@example.com', 'Kiro Github 账号-3'])
    const expiry = Date.parse(String(payload.accounts[0].credentials.expires_at))
    expect(expiry).toBeGreaterThanOrEqual(before - 1000)
    expect(expiry).toBeLessThanOrEqual(Date.now())
  })

  it('infers IdC from client credentials when authMethod is missing', () => {
    const { payload } = convertKiroAccountManagerExport([{ ...idc, authMethod: null }])
    expect(payload.accounts[0].credentials.auth_method).toBe('idc')
  })

  it('skips unusable, unsupported and duplicate records', () => {
    const { payload, skipped } = convertKiroAccountManagerExport([
      social,
      { ...social, id: 'dup', label: 'dup' },
      { ...social, label: 'banned', refreshToken: 'rt-3', status: 'banned' },
      { ...social, label: 'ms', refreshToken: 'rt-4', authMethod: 'external_idp' },
      { ...social, label: 'empty', accessToken: '', refreshToken: 'rt-5' }
    ])
    expect(payload.accounts.map((a) => a.name)).toEqual(['Kiro BuilderId 账号'])
    expect(skipped.map((s) => [s.name, s.reason])).toEqual([
      ['dup', 'duplicate'], ['banned', 'unusable_status'], ['ms', 'unsupported_auth'], ['empty', 'missing_token']
    ])
  })
})
