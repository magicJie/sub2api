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
  /** 平台 -> 每个代理允许绑定的历史账号数上限；未配置的平台不限 */
  platform_limits: Record<string, number>
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
  platform_limits?: Record<string, number>
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

export const proxyPoolAPI = {
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
