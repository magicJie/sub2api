// 前置组（fork）：前置组列表与选项，供落地代理的前置选择使用。
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { upstreamGroupsAPI, type UpstreamGroup } from './api'

export const UPSTREAM_ERROR_NAMESPACE = 'admin.proxyUpstream.errors'

export function useUpstreamGroups() {
  const { t } = useI18n()
  const groups = ref<UpstreamGroup[]>([])
  const error = ref('')
  async function loadUpstreamGroups() {
    try {
      groups.value = await upstreamGroupsAPI.list()
      error.value = ''
    } catch {
      error.value = t('admin.proxyUpstream.loadFailed')
    }
  }
  onMounted(loadUpstreamGroups)
  // 停用的组仍列出（编辑已引用停用组的代理时可见），但不可选
  const groupOptions = computed(() =>
    groups.value.map((group) => ({
      value: group.id,
      label: `${group.name}${group.status === 'inactive' ? ` (${t('admin.proxyUpstream.inactive')})` : ''}`,
      disabled: group.status !== 'active'
    }))
  )
  return { groups, groupOptions, loadUpstreamGroups, error }
}
