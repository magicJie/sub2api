/**
 * 代理池（fork）管理端 API：代理池分组、池内代理与退役状态、账号-代理绑定记录、账号分配/解绑。
 * 后端见 backend/internal/handler/admin/proxy_pool_handler.go。
 */

import { apiClient } from '@/api/client'
import type { Proxy } from '@/types'

export interface ProxyPool {
  id: number
  name: string
  description: string
  is_default: boolean
  proxy_count: number
  created_at: string
  updated_at: string
}

export interface ProxyPoolMember {
  proxy: Proxy
  /** 平台 -> 历史累计绑定过的不同账号数（含已解绑、已删除） */
  history_counts: Record<string, number>
  /** 平台 -> 当前仍绑定的账号数 */
  active_counts: Record<string, number>
  /** 已达上限、不再分配的平台 */
  retired_platforms: string[]
}

export interface ProxyAccountBinding {
  id: number
  proxy_id: number
  proxy_name: string
  account_id: number
  account_name: string
  platform: string
  pool_id: number | null
  pool_name: string
  source: 'manual' | 'pool'
  locked: boolean
  bound_at: string
  unlocked_at: string | null
  unbound_at: string | null
  unbind_reason: '' | 'proxy_changed' | 'account_deleted'
}

export interface ProxyBindingsResult {
  bindings: ProxyAccountBinding[]
  summary: Record<string, { history: number; active: number }>
  platforms: string[]
}

export interface ProxyPoolUpsert {
  name?: string
  description?: string
}

/** 平台 IP 策略：churn 消耗型（按历史账号数退役）/ sticky 长期型（账号终身固定一个落地 IP） */
export type ProxyIPPolicyMode = 'churn' | 'sticky'

export interface ProxyIPPolicy {
  id: number
  platform: string
  mode: ProxyIPPolicyMode
  /** 可取 IP 的代理池，空数组为全部代理池 */
  pool_ids: number[]
  /** 每个落地 IP 对该平台的历史累计账号上限，null 不限 */
  max_lifetime_accounts: number | null
  /** 每个落地 IP 对该平台同时在用的账号上限，null 不限 */
  max_active_accounts: number | null
  on_egress_failure: 'pause' | 'reassign'
  created_at: string
  updated_at: string
}

export type ProxyIPPolicyUpsert = Pick<
  ProxyIPPolicy,
  'mode' | 'pool_ids' | 'max_lifetime_accounts' | 'max_active_accounts' | 'on_egress_failure'
>

export async function listPolicies(): Promise<ProxyIPPolicy[]> {
  const { data } = await apiClient.get<ProxyIPPolicy[]>('/admin/proxy-ip-policies')
  return Array.isArray(data) ? data : []
}

export async function upsertPolicy(platform: string, payload: ProxyIPPolicyUpsert): Promise<ProxyIPPolicy> {
  const { data } = await apiClient.put<ProxyIPPolicy>(
    `/admin/proxy-ip-policies/${encodeURIComponent(platform)}`,
    payload
  )
  return data
}

export async function deletePolicy(platform: string): Promise<void> {
  await apiClient.delete(`/admin/proxy-ip-policies/${encodeURIComponent(platform)}`)
}

export async function listPools(): Promise<ProxyPool[]> {
  const { data } = await apiClient.get<ProxyPool[]>('/admin/proxy-pools')
  return Array.isArray(data) ? data : []
}

export async function createPool(payload: ProxyPoolUpsert): Promise<ProxyPool> {
  const { data } = await apiClient.post<ProxyPool>('/admin/proxy-pools', payload)
  return data
}

export async function updatePool(id: number, payload: ProxyPoolUpsert): Promise<ProxyPool> {
  const { data } = await apiClient.put<ProxyPool>(`/admin/proxy-pools/${id}`, payload)
  return data
}

export async function deletePool(id: number): Promise<void> {
  await apiClient.delete(`/admin/proxy-pools/${id}`)
}

export async function listMembers(id: number): Promise<{ pool: ProxyPool; members: ProxyPoolMember[] }> {
  const { data } = await apiClient.get<{ pool: ProxyPool; members: ProxyPoolMember[] }>(
    `/admin/proxy-pools/${id}/proxies`
  )
  return data
}

export async function assignProxies(id: number, proxyIds: number[]): Promise<void> {
  await apiClient.post(`/admin/proxy-pools/${id}/proxies`, { proxy_ids: proxyIds })
}

/** 下一次分配会选中的代理（不占用）；OAuth 授权需先用它换取 token。 */
export async function previewProxy(id: number, platform: string): Promise<Proxy> {
  const { data } = await apiClient.get<Proxy>(`/admin/proxy-pools/${id}/preview`, {
    params: { platform }
  })
  return data
}

export async function getProxyBindings(proxyId: number): Promise<ProxyBindingsResult> {
  const { data } = await apiClient.get<ProxyBindingsResult>(`/admin/proxies/${proxyId}/bindings`)
  return data
}

export async function getAccountBinding(accountId: number): Promise<ProxyAccountBinding | null> {
  const { data } = await apiClient.get<ProxyAccountBinding | null>(
    `/admin/accounts/${accountId}/proxy-binding`
  )
  return data ?? null
}

export async function assignAccountFromPool(accountId: number, poolId: number): Promise<Proxy> {
  const { data } = await apiClient.post<Proxy>(`/admin/accounts/${accountId}/proxy-pool`, {
    pool_id: poolId
  })
  return data
}

export async function unbindAccountProxy(accountId: number): Promise<void> {
  await apiClient.post(`/admin/accounts/${accountId}/proxy-unbind`)
}

/** 落地 IP 故障：egress 落地 IP 不通（按策略处置账号）；transport 前置链路不可用（只告警） */
export interface ProxyEgressIncidentAccount {
  account_id: number
  account_name: string
  platform: string
  action: 'paused' | 'reassigned' | 'alert_only'
  to_proxy_id: number | null
  message: string
  created_at: string
  resumed_at: string | null
  schedulable: boolean
  current_proxy_id: number | null
}

export interface ProxyEgressIncident {
  id: number
  proxy_id: number
  proxy_name: string
  layer: 'egress' | 'transport'
  status: 'open' | 'resolved'
  last_error: string
  fail_count: number
  first_failed_at: string
  last_checked_at: string
  resolved_at: string | null
  handled_at: string | null
  accounts: ProxyEgressIncidentAccount[]
}

export interface ProxyReplaceCandidate {
  proxy: Proxy
  pool_id: number
  pool_name: string
  history_counts: Record<string, number>
  active_counts: Record<string, number>
  country: string
  same_country: boolean
}

export interface ProxyMigrateResult {
  account_id: number
  to_proxy_id: number | null
  error?: string
}

export async function listIncidents(): Promise<ProxyEgressIncident[]> {
  const { data } = await apiClient.get<ProxyEgressIncident[]>('/admin/proxy-incidents')
  return Array.isArray(data) ? data : []
}

export async function getReplaceCandidates(accountId: number): Promise<ProxyReplaceCandidate[]> {
  const { data } = await apiClient.get<ProxyReplaceCandidate[]>(`/admin/accounts/${accountId}/proxy-replace-candidates`)
  return Array.isArray(data) ? data : []
}

/** 换落地 IP 并锁定；proxyId 省略时自动选第一个候选。因故障暂停的账号会同时恢复调度。 */
export async function replaceAccountProxy(accountId: number, proxyId?: number): Promise<Proxy> {
  const { data } = await apiClient.post<Proxy>(`/admin/accounts/${accountId}/proxy-replace`, proxyId ? { proxy_id: proxyId } : {})
  return data
}

/** 落地 IP 已恢复：解除故障暂停、恢复调度，不换 IP */
export async function resumeAccountProxy(accountId: number): Promise<void> {
  await apiClient.post(`/admin/accounts/${accountId}/proxy-resume`)
}

export async function migrateProxyAccounts(proxyId: number): Promise<ProxyMigrateResult[]> {
  const { data } = await apiClient.post<ProxyMigrateResult[]>(`/admin/proxies/${proxyId}/migrate-accounts`)
  return Array.isArray(data) ? data : []
}

/** 立即巡检一次，返回当前未恢复的故障（无故障为 null） */
export async function checkProxyHealth(proxyId: number): Promise<{ id: number; layer: string; fail_count: number; last_error: string; handled: boolean } | null> {
  const { data } = await apiClient.post<{ id: number; layer: string; fail_count: number; last_error: string; handled: boolean } | null>(
    `/admin/proxies/${proxyId}/health-check`
  )
  return data ?? null
}

/** 落地 IP 巡检配置：关闭后不巡检、不处置；修改在下一个调度周期生效 */
export interface ProxyEgressHealthConfig {
  enabled: boolean
  interval_seconds: number
  fail_threshold: number
}

export async function getEgressHealthConfig(): Promise<ProxyEgressHealthConfig> {
  const { data } = await apiClient.get<ProxyEgressHealthConfig>('/admin/proxy-incidents/config')
  return data
}

export async function updateEgressHealthConfig(cfg: ProxyEgressHealthConfig): Promise<ProxyEgressHealthConfig> {
  const { data } = await apiClient.put<ProxyEgressHealthConfig>('/admin/proxy-incidents/config', cfg)
  return data
}

export const proxyPoolAPI = {
  getEgressHealthConfig,
  updateEgressHealthConfig,
  listIncidents,
  getReplaceCandidates,
  replaceAccountProxy,
  resumeAccountProxy,
  migrateProxyAccounts,
  checkProxyHealth,
  listPolicies,
  upsertPolicy,
  deletePolicy,
  listPools,
  createPool,
  updatePool,
  deletePool,
  listMembers,
  assignProxies,
  previewProxy,
  getProxyBindings,
  getAccountBinding,
  assignAccountFromPool,
  unbindAccountProxy
}

export default proxyPoolAPI
