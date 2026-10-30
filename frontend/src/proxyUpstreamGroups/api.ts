// 前置组（fork）：AIO 实例、AIO 端口（前置端点，AIO 的一个监听端口）与前置组（有序槽位）管理接口；AIO 密码从不回传。
import { apiClient } from '@/api/client'

export type UpstreamEndpointProtocol = 'socks5' | 'socks5h' | 'http' | 'https'
export type UpstreamStatus = 'active' | 'inactive'

export interface UpstreamEndpointHealth {
  state: 'unknown' | 'healthy' | 'unhealthy'
  consecutive_failures: number
  consecutive_successes: number
  healthy_since?: string
  last_checked_at?: string
  last_error?: string
  latency_ms: number
}
/** AIO 实例：主机、协议与认证由其下所有端口共用 */
export interface UpstreamAIO {
  id: number
  name: string
  protocol: UpstreamEndpointProtocol
  host: string
  username: string
  has_password: boolean
  status: UpstreamStatus
  created_at: string
  updated_at: string
  port_count: number
}
/** 更新时 password 省略或空串表示保持不变 */
export interface UpstreamAIOInput {
  name: string
  protocol: UpstreamEndpointProtocol
  host: string
  username: string
  password?: string
  status: UpstreamStatus
}
/** AIO 端口；protocol / host / username 取自所属 AIO，只读 */
export interface UpstreamEndpoint {
  id: number
  aio_id: number
  aio_name: string
  aio_status: UpstreamStatus
  name: string
  protocol: UpstreamEndpointProtocol
  host: string
  port: number
  username: string
  status: UpstreamStatus
  created_at: string
  updated_at: string
  health: UpstreamEndpointHealth
  /** 引用该端口的前置组名 */
  group_names: string[]
}
export interface UpstreamEndpointInput {
  aio_id: number
  name: string
  port: number
  status: UpstreamStatus
}

export interface UpstreamSlotRuntime {
  available: boolean
  current_endpoint_id: number | null
  available_since?: string
}
export interface UpstreamSlot {
  id: number
  name: string
  position: number
  /** 1-100，落地按权重比例分到各槽位 */
  weight: number
  /** 有序端口列表：序号 1 为主，其余按序备用 */
  endpoint_ids: number[]
  runtime?: UpstreamSlotRuntime
}
export interface UpstreamGroup {
  id: number
  name: string
  status: UpstreamStatus
  probe_url: string
  probe_interval_seconds: number
  fail_threshold: number
  recover_threshold: number
  failback_seconds: number
  created_at: string
  updated_at: string
  /** 引用该组的落地代理数 */
  proxy_count: number
  slots: UpstreamSlot[]
}
/** 槽位顺序即数组顺序；至少 1 个槽位，每个槽位至少 1 个端点、槽位内不重复 */
export interface UpstreamGroupInput {
  name: string
  status: UpstreamStatus
  probe_url: string
  probe_interval_seconds: number
  fail_threshold: number
  recover_threshold: number
  failback_seconds: number
  slots: { id?: number; name: string; weight: number; endpoint_ids: number[] }[]
}

const aioRoot = '/admin/proxy-upstream-aios'
export const upstreamAIOsAPI = {
  async list(): Promise<UpstreamAIO[]> {
    return (await apiClient.get<UpstreamAIO[]>(aioRoot)).data
  },
  async create(input: UpstreamAIOInput): Promise<UpstreamAIO> {
    return (await apiClient.post<UpstreamAIO>(aioRoot, input)).data
  },
  async update(id: number, input: UpstreamAIOInput): Promise<UpstreamAIO> {
    return (await apiClient.put<UpstreamAIO>(`${aioRoot}/${id}`, input)).data
  },
  async remove(id: number): Promise<void> {
    await apiClient.delete(`${aioRoot}/${id}`)
  }
}

const endpointRoot = '/admin/proxy-upstream-endpoints'
export const upstreamEndpointsAPI = {
  async list(): Promise<UpstreamEndpoint[]> {
    return (await apiClient.get<UpstreamEndpoint[]>(endpointRoot)).data
  },
  async get(id: number): Promise<UpstreamEndpoint> {
    return (await apiClient.get<UpstreamEndpoint>(`${endpointRoot}/${id}`)).data
  },
  async create(input: UpstreamEndpointInput): Promise<UpstreamEndpoint> {
    return (await apiClient.post<UpstreamEndpoint>(endpointRoot, input)).data
  },
  async update(id: number, input: UpstreamEndpointInput): Promise<UpstreamEndpoint> {
    return (await apiClient.put<UpstreamEndpoint>(`${endpointRoot}/${id}`, input)).data
  },
  async remove(id: number): Promise<void> {
    await apiClient.delete(`${endpointRoot}/${id}`)
  }
}

const groupRoot = '/admin/proxy-upstream-groups'
export const upstreamGroupsAPI = {
  async list(): Promise<UpstreamGroup[]> {
    return (await apiClient.get<UpstreamGroup[]>(groupRoot)).data
  },
  async get(id: number): Promise<UpstreamGroup> {
    return (await apiClient.get<UpstreamGroup>(`${groupRoot}/${id}`)).data
  },
  async create(input: UpstreamGroupInput): Promise<UpstreamGroup> {
    return (await apiClient.post<UpstreamGroup>(groupRoot, input)).data
  },
  async update(id: number, input: UpstreamGroupInput): Promise<UpstreamGroup> {
    return (await apiClient.put<UpstreamGroup>(`${groupRoot}/${id}`, input)).data
  },
  async remove(id: number): Promise<void> {
    await apiClient.delete(`${groupRoot}/${id}`)
  }
}
