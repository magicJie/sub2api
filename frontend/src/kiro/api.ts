/**
 * Kiro 平台管理端 API 封装。
 *
 * 对应后端 `internal/handler/admin/kiro_oauth_handler.go` 的 5 个 OAuth 相关接口。
 */
import { apiClient } from '@/api/client'

export interface KiroAuthUrlResponse {
  auth_url: string
  session_id: string
  state: string
}

export interface KiroIDCAuthUrlResponse extends KiroAuthUrlResponse {
  client_id?: string
  region?: string
  start_url?: string
}

export interface KiroTokenInfo {
  access_token?: string
  refresh_token?: string
  profile_arn?: string
  expires_at?: string
  auth_method?: string
  provider?: string
  client_id?: string
  client_secret?: string
  client_id_hash?: string
  email?: string
  start_url?: string
  region?: string
  /** 仅前端导入时从 Kiro Account Manager 导出中带出，后端不返回。 */
  machine_id?: string
  [key: string]: unknown
}

export async function generateAuthUrl(payload: {
  proxy_id?: number
  provider?: string
}): Promise<KiroAuthUrlResponse> {
  const { data } = await apiClient.post<KiroAuthUrlResponse>('/admin/kiro/oauth/auth-url', payload)
  return data
}

export async function generateIDCAuthUrl(payload: {
  proxy_id?: number
  start_url?: string
  region?: string
}): Promise<KiroIDCAuthUrlResponse> {
  const { data } = await apiClient.post<KiroIDCAuthUrlResponse>('/admin/kiro/oauth/idc-auth-url', payload)
  return data
}

export async function exchangeCode(payload: {
  session_id: string
  state: string
  code: string
  callback_path?: string
  login_option?: string
  proxy_id?: number
}): Promise<KiroTokenInfo> {
  const { data } = await apiClient.post<KiroTokenInfo>('/admin/kiro/oauth/exchange-code', payload)
  return data
}

export async function refreshToken(payload: {
  refresh_token: string
  auth_method?: string
  provider?: string
  client_id?: string
  client_secret?: string
  start_url?: string
  region?: string
  profile_arn?: string
  proxy_id?: number
}): Promise<KiroTokenInfo> {
  const { data } = await apiClient.post<KiroTokenInfo>('/admin/kiro/oauth/refresh-token', payload)
  return data
}

export async function importToken(payload: {
  token_json: string
  device_registration_json?: string
}): Promise<KiroTokenInfo> {
  const { data } = await apiClient.post<KiroTokenInfo>('/admin/kiro/oauth/import-token', payload)
  return data
}

/** Kiro 额度后台刷新设置（后端 service/kiro_usage_refresher.go）。 */
export interface KiroUsageRefreshSettings {
  enabled: boolean
  interval_minutes: number
  concurrency: number
}

export async function getUsageRefreshSettings(): Promise<KiroUsageRefreshSettings> {
  const { data } = await apiClient.get<KiroUsageRefreshSettings>('/admin/kiro/usage-refresh/settings')
  return data
}

export async function updateUsageRefreshSettings(
  settings: KiroUsageRefreshSettings
): Promise<KiroUsageRefreshSettings> {
  const { data } = await apiClient.put<KiroUsageRefreshSettings>('/admin/kiro/usage-refresh/settings', settings)
  return data
}

/** 把所有 Kiro 账号排进后台刷新队列，立即返回排队数量，不等待上游。 */
export async function refreshAllUsage(): Promise<{ queued: number }> {
  const { data } = await apiClient.post<{ queued: number }>('/admin/kiro/usage-refresh/refresh-all')
  return data
}

export default {
  generateAuthUrl,
  generateIDCAuthUrl,
  exchangeCode,
  refreshToken,
  importToken,
  getUsageRefreshSettings,
  updateUsageRefreshSettings,
  refreshAllUsage
}
