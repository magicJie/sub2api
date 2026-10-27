// 链式代理（fork）：首跳组管理接口；subscription_urls 的读取值始终是掩码。
import { apiClient } from '@/api/client'

export interface HopGroupRuntime {
  selected: string
  nodes: {
    name: string
    alive: boolean
    delay_ms: number
    active_connections: number
  }[]
  providers: {
    name: string
    updated_at: string
    node_count: number
    last_error?: string
  }[]
}
export interface HopGroup {
  id: number
  name: string
  strategy: 'fallback' | 'url-test' | 'load-balance'
  test_url: string
  interval_seconds: number
  tolerance_ms: number
  subscription_urls: string[]
  subscription_count: number
  member_proxy_ids: number[]
  subscription_update_interval: number
  status: 'active' | 'inactive'
  created_at: string
  updated_at: string
  runtime?: HopGroupRuntime
  runtime_error?: string
}
export type HopGroupInput = Pick<
  HopGroup,
  | 'name'
  | 'strategy'
  | 'test_url'
  | 'interval_seconds'
  | 'tolerance_ms'
  | 'member_proxy_ids'
  | 'subscription_update_interval'
  | 'status'
> & { subscription_urls?: string[] }

const root = '/admin/proxy-hop-groups'
export const hopGroupsAPI = {
  async list(): Promise<HopGroup[]> {
    return (await apiClient.get<HopGroup[]>(root)).data
  },
  async get(id: number): Promise<HopGroup> {
    return (await apiClient.get<HopGroup>(`${root}/${id}`)).data
  },
  async create(input: HopGroupInput): Promise<HopGroup> {
    return (await apiClient.post<HopGroup>(root, input)).data
  },
  async update(id: number, input: HopGroupInput): Promise<HopGroup> {
    return (await apiClient.put<HopGroup>(`${root}/${id}`, input)).data
  },
  async remove(id: number): Promise<void> {
    await apiClient.delete(`${root}/${id}`)
  }
}
