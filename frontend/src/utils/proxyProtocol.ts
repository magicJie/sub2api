// 代理协议(含加密协议 ss/vmess/vless/trojan)的表单字段规则。
// 纯函数,无 i18n/组件依赖:ProxiesView 与 ProxyProtocolOptionsFields 共用,便于单测。
import type { ProxyProtocol } from '@/types'

export const PLAIN_PROTOCOLS = ['http', 'https', 'socks5', 'socks5h'] as const
export const ENCRYPTED_PROTOCOLS = ['ss', 'vmess', 'vless', 'trojan'] as const
export const ALL_PROXY_PROTOCOLS: ProxyProtocol[] = [...PLAIN_PROTOCOLS, ...ENCRYPTED_PROTOCOLS]

export const isEncryptedProtocol = (protocol: string): boolean =>
  (ENCRYPTED_PROTOCOLS as readonly string[]).includes(protocol)

/** vmess/vless 的 password 字段实际承载 UUID。 */
export const usesUuidSecret = (protocol: string): boolean =>
  protocol === 'vmess' || protocol === 'vless'

/** 带传输层/TLS 参数的协议。 */
export const hasTransport = (protocol: string): boolean =>
  protocol === 'vmess' || protocol === 'vless' || protocol === 'trojan'

export const SS_METHODS = [
  'aes-128-gcm',
  'aes-256-gcm',
  'chacha20-ietf-poly1305',
  'xchacha20-ietf-poly1305',
  '2022-blake3-aes-128-gcm',
  '2022-blake3-aes-256-gcm',
  '2022-blake3-chacha20-poly1305'
]
export const VMESS_SECURITIES = ['auto', 'aes-128-gcm', 'chacha20-poly1305', 'none']
export const VLESS_FLOWS = ['', 'xtls-rprx-vision']
export const NETWORKS = ['tcp', 'ws', 'grpc', 'httpupgrade', 'xhttp']
export const XHTTP_MODES = ['auto', 'packet-up', 'stream-up', 'stream-one']
export const TLS_MODES = ['none', 'tls', 'reality']
export const FINGERPRINTS = ['chrome', 'firefox', 'safari', 'edge', 'ios', 'android', 'random', 'randomized']

export const SHARE_LINK_PREFIXES = ['ss://', 'vmess://', 'vless://', 'trojan://']

/** 是否为加密协议分享链接(ss:// vmess:// vless:// trojan://),大小写不敏感。 */
export const isShareLink = (line: string): boolean => {
  const lower = line.trim().toLowerCase()
  return SHARE_LINK_PREFIXES.some((prefix) => lower.startsWith(prefix))
}

export type ProxyOptions = Record<string, string>

const PATH_NETWORKS = ['ws', 'httpupgrade', 'xhttp']

/** 合并协议默认值后的有效参数(用于表单展示与提交)。 */
export function resolveProxyOptions(protocol: string, raw?: ProxyOptions | null): ProxyOptions {
  const src = raw ?? {}
  const pick = (key: string, fallback: string) => src[key] ?? fallback
  const out: ProxyOptions = {}
  if (protocol === 'ss') {
    out.method = pick('method', '')
    return out
  }
  if (!hasTransport(protocol)) return out

  if (protocol === 'vmess') out.security = pick('security', 'auto')
  if (protocol === 'vless') {
    out.flow = pick('flow', '')
    out.encryption = pick('encryption', 'none')
  }

  out.network = pick('network', 'tcp')
  if (PATH_NETWORKS.includes(out.network)) {
    out.path = pick('path', '')
    out.host = pick('host', '')
  }
  if (out.network === 'grpc') out.service_name = pick('service_name', '')
  if (out.network === 'xhttp') out.mode = pick('mode', 'auto')

  out.tls = pick('tls', protocol === 'trojan' ? 'tls' : 'none')
  if (out.tls !== 'none') {
    out.sni = pick('sni', '')
    out.alpn = pick('alpn', '')
    // reality 必须带 fingerprint,默认 chrome;普通 tls 留空走后端默认
    out.fingerprint = pick('fingerprint', out.tls === 'reality' ? 'chrome' : '')
  }
  if (out.tls === 'reality') {
    out.public_key = pick('public_key', '')
    out.short_id = pick('short_id', '')
    out.spider_x = pick('spider_x', '')
  }
  return out
}

/** 提交给后端的 options:仅保留当前协议/模式适用的键,去掉空值并 trim。 */
export function buildProxyOptions(protocol: string, raw?: ProxyOptions | null): ProxyOptions {
  const resolved = resolveProxyOptions(protocol, raw)
  const out: ProxyOptions = {}
  for (const [key, value] of Object.entries(resolved)) {
    const trimmed = (value ?? '').trim()
    if (trimmed) out[key] = trimmed
  }
  return out
}

/**
 * 校验加密协议必填项,返回 i18n key(admin.proxies.* 下)或 null。
 * password 为本次将提交/保留的密钥;编辑时未修改密码可传 keepExistingSecret=true 跳过。
 */
export function validateProxySecret(
  protocol: string,
  password: string,
  options: ProxyOptions,
  keepExistingSecret = false
): string | null {
  if (!isEncryptedProtocol(protocol)) return null
  if (!keepExistingSecret && !password.trim()) {
    return usesUuidSecret(protocol) ? 'admin.proxies.uuidRequired' : 'admin.proxies.secretRequired'
  }
  if (protocol === 'ss' && !options.method) return 'admin.proxies.ssMethodRequired'
  if (options.tls === 'reality' && !options.public_key) return 'admin.proxies.realityPublicKeyRequired'
  return null
}
