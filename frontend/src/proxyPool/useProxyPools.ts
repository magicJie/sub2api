import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import proxyPoolAPI, { type ProxyPool } from './api'

/**
 * 代理池（fork）：代理池下拉选项。
 * 代理表单用 null 表示默认池（后端把默认池存为 NULL）。
 */
export function useProxyPoolOptions() {
  const { t } = useI18n()
  const pools = ref<ProxyPool[]>([])

  const loadPools = async () => {
    try {
      pools.value = await proxyPoolAPI.listPools()
    } catch {
      pools.value = []
    }
  }

  const poolLabel = (pool: ProxyPool) =>
    pool.is_default ? `${pool.name}（${t('admin.proxyPools.defaultPool')}）` : pool.name

  /** 代理表单：默认池为 null */
  const poolOptions = computed(() =>
    pools.value.map((pool) => ({ value: pool.is_default ? null : pool.id, label: poolLabel(pool) }))
  )

  /** 账号创建：首项“不使用代理池”为 null，其余为真实池 id */
  const accountPoolOptions = computed(() => [
    { value: null, label: t('admin.proxyPools.noPool') },
    ...pools.value.map((pool) => ({ value: pool.id, label: poolLabel(pool) }))
  ])

  return { pools, loadPools, poolOptions, accountPoolOptions }
}
