import { ref } from 'vue'

// 账号页启用的平台（存浏览器本地）：平台筛选下拉只列出这些平台。
// null 表示未配置，全部平台都启用。
export const ACCOUNT_ENABLED_PLATFORMS_STORAGE_KEY = 'account-enabled-platforms'

const load = (): string[] | null => {
  try {
    const raw = localStorage.getItem(ACCOUNT_ENABLED_PLATFORMS_STORAGE_KEY)
    if (!raw) return null
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return null
    const list = parsed.filter((item): item is string => typeof item === 'string')
    return list.length ? list : null
  } catch {
    return null
  }
}

export const accountEnabledPlatforms = ref<string[] | null>(load())

export const isAccountPlatformEnabled = (platform: string): boolean =>
  accountEnabledPlatforms.value === null || accountEnabledPlatforms.value.includes(platform)

export const saveAccountEnabledPlatforms = (platforms: string[] | null) => {
  accountEnabledPlatforms.value = platforms && platforms.length ? [...platforms] : null
  try {
    if (accountEnabledPlatforms.value) {
      localStorage.setItem(ACCOUNT_ENABLED_PLATFORMS_STORAGE_KEY, JSON.stringify(accountEnabledPlatforms.value))
    } else {
      localStorage.removeItem(ACCOUNT_ENABLED_PLATFORMS_STORAGE_KEY)
    }
  } catch {
    // 存储不可用时只在当前页面生效。
  }
}
