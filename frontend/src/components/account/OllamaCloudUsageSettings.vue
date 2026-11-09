<template>
  <section v-if="state?.eligible" class="space-y-4 border-t border-gray-200 pt-4 dark:border-dark-600" data-testid="ollama-cloud-usage-settings">
    <div class="flex items-start justify-between gap-4">
      <div>
        <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('admin.accounts.ollamaCloud.title') }}
        </h3>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.ollamaCloud.apiUsageHint') }}
        </p>
      </div>
      <span
        class="whitespace-nowrap rounded px-2 py-1 text-xs font-medium"
        :class="state.configured
          ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
          : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'"
      >
        {{ state.configured ? t('admin.accounts.ollamaCloud.configured') : t('admin.accounts.ollamaCloud.notConfigured') }}
      </span>
    </div>

    <div v-if="loading" class="flex h-20 items-center justify-center text-gray-400">
      <Icon name="refresh" size="sm" class="animate-spin" />
    </div>
    <template v-else>
      <div
        v-if="snapshot"
        class="border-y border-gray-100 py-3 dark:border-dark-700"
        data-testid="ollama-cloud-usage-details"
      >
        <div class="grid grid-cols-[minmax(4rem,auto)_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-xs">
          <template v-if="snapshot.data?.five_hour">
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.ollamaCloud.fiveHour') }}</span>
            <span class="break-words text-gray-900 dark:text-white">{{ windowSummary(snapshot.data.five_hour) }}</span>
          </template>
          <template v-if="snapshot.data?.seven_day">
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.ollamaCloud.sevenDay') }}</span>
            <span class="break-words text-gray-900 dark:text-white">{{ windowSummary(snapshot.data.seven_day) }}</span>
          </template>
          <template v-if="snapshot.data?.monthly">
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.ollamaCloud.monthly') }}</span>
            <span class="break-words text-gray-900 dark:text-white">{{ windowSummary(snapshot.data.monthly) }}</span>
          </template>
          <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.ollamaCloud.balance') }}</span>
          <span class="break-words text-gray-900 dark:text-white">{{ snapshot.data?.balance || '-' }}</span>
          <template v-if="snapshot.data?.usage">
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.ollamaCloud.usageRange') }}</span>
            <span class="break-words text-gray-900 dark:text-white" data-testid="ollama-cloud-usage-range">{{ usageRangeSummary }}</span>
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.ollamaCloud.usageTotals') }}</span>
            <span class="break-words text-gray-900 dark:text-white" data-testid="ollama-cloud-usage-totals">{{ usageTotalsSummary }}</span>
          </template>
          <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.ollamaCloud.models') }}</span>
          <span class="break-words text-gray-900 dark:text-white" data-testid="ollama-cloud-usage-models">{{ modelSummary }}</span>
          <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.ollamaCloud.status') }}</span>
          <span class="break-words font-medium text-gray-900 dark:text-white">{{ statusLabel }}</span>
          <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.ollamaCloud.updatedAt') }}</span>
          <span class="break-words text-gray-900 dark:text-white">{{ formatDate(snapshot.fetched_at || snapshot.last_attempt_at) }}</span>
        </div>
        <p
          v-if="snapshotError"
          class="mt-2 break-words border-t border-gray-100 pt-2 text-xs text-amber-700 dark:border-dark-700 dark:text-amber-300"
          data-testid="ollama-cloud-usage-error"
        >
          {{ snapshotError }}
          <span v-if="hasUsageData">{{ t('admin.accounts.ollamaCloud.staleData') }}</span>
        </p>
      </div>

      <p
        v-if="!state.configured"
        class="text-xs text-gray-500 dark:text-gray-400"
        data-testid="ollama-cloud-usage-not-configured"
      >
        {{ t('admin.accounts.ollamaCloud.cellNotConfigured') }}
      </p>

      <div v-if="state.configured" class="flex flex-wrap items-center gap-2">
        <button
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="refreshing"
          data-testid="ollama-cloud-refresh"
          @click="refreshUsage"
        >
          <Icon name="refresh" size="xs" class="mr-1.5" :class="{ 'animate-spin': refreshing }" />
          {{ t('admin.accounts.ollamaCloud.refreshNow') }}
        </button>
      </div>

      <div v-if="state.configured" class="flex items-center justify-between gap-4 border-t border-gray-100 pt-4 dark:border-dark-700">
        <div>
          <label class="text-sm font-medium text-gray-900 dark:text-white">
            {{ t('admin.accounts.ollamaCloud.autoRefresh') }}
          </label>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.ollamaCloud.autoRefreshHint') }}
          </p>
        </div>
        <Toggle
          :model-value="state.auto_refresh_enabled"
          :disabled="saving"
          data-testid="ollama-cloud-auto-refresh"
          @update:model-value="setAutoRefresh"
        />
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage, extractI18nErrorMessage } from '@/utils/apiError'
import type { Account, OllamaCloudUsageMetrics, OllamaCloudUsageState, OllamaCloudUsageWindow } from '@/types'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatOllamaSnapshotError } from '@/utils/ollamaUsage'

const props = defineProps<{ account: Account }>()
const emit = defineEmits<{ updated: [state: OllamaCloudUsageState] }>()
const { t } = useI18n()
const appStore = useAppStore()
const state = ref<OllamaCloudUsageState | null>(props.account.ollama_cloud_usage ?? null)
const loading = ref(false)
const saving = ref(false)
const refreshing = ref(false)
const snapshot = computed(() => state.value?.snapshot)
const hasUsageData = computed(() => !!(
  snapshot.value?.data?.five_hour ||
  snapshot.value?.data?.seven_day ||
  snapshot.value?.data?.monthly ||
  snapshot.value?.data?.balance ||
  snapshot.value?.data?.usage
))
const snapshotError = computed(() => formatOllamaSnapshotError(snapshot.value, t))
const statusLabel = computed(() => {
  if (!snapshot.value) return t('admin.accounts.ollamaCloud.notRefreshed')
  if (snapshot.value.status === 'unauthorized') return t('admin.accounts.ollamaCloud.unauthorized')
  if (snapshot.value.status === 'failed') {
    return snapshot.value.http_status
      ? `${t('admin.accounts.ollamaCloud.failed')} (HTTP ${snapshot.value.http_status})`
      : t('admin.accounts.ollamaCloud.failed')
  }
  return t('admin.accounts.ollamaCloud.ok')
})

const formatUsd = (value: number) => {
  const fixed = value.toFixed(4).replace(/\.?0+$/, '')
  return `$${fixed || '0'}`
}
const formatMetrics = (metrics?: OllamaCloudUsageMetrics) => {
  if (!metrics) return '-'
  const parts = [`${metrics.request_count} ${t('admin.accounts.ollamaCloud.requests')}`]
  if (typeof metrics.usage_usd === 'number' && Number.isFinite(metrics.usage_usd)) {
    parts.push(formatUsd(metrics.usage_usd))
  }
  const tokenParts: string[] = []
  if (typeof metrics.input_tokens === 'number') {
    tokenParts.push(`${t('admin.accounts.ollamaCloud.inputTokens')} ${metrics.input_tokens}`)
  }
  if (typeof metrics.cached_input_tokens === 'number') {
    tokenParts.push(`${t('admin.accounts.ollamaCloud.cachedInputTokens')} ${metrics.cached_input_tokens}`)
  }
  if (typeof metrics.output_tokens === 'number') {
    tokenParts.push(`${t('admin.accounts.ollamaCloud.outputTokens')} ${metrics.output_tokens}`)
  }
  if (tokenParts.length > 0) {
    parts.push(tokenParts.join(' / '))
  }
  return parts.join(' · ')
}

const formatUtcRangeDate = (value?: string) => {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toISOString().replace('T', ' ').replace(/\.\d+Z$/, ' UTC')
}

const usageRangeSummary = computed(() => {
  const usage = snapshot.value?.data?.usage
  if (!usage) return '-'
  const rangeLabel = usage.range
  return `${rangeLabel} (${formatUtcRangeDate(usage.from)} – ${formatUtcRangeDate(usage.until)})`
})

const usageTotalsSummary = computed(() => formatMetrics(snapshot.value?.data?.usage?.totals))

const modelSummary = computed(() => {
  const usage = snapshot.value?.data?.usage
  if (!usage) return '-'
  const rangeLabel = usage.range
  const items = (usage.totals?.groups ?? []).map(
    group => `${rangeLabel} ${group.model}: ${formatMetrics(group)}`
  )
  if (usage.totals?.remainder) {
    items.push(`${rangeLabel} ${t('admin.accounts.ollamaCloud.otherModels')}: ${formatMetrics(usage.totals.remainder)}`)
  }
  return items.join(', ') || '-'
})

const formatPercent = (value?: number) => typeof value === 'number' && Number.isFinite(value)
  ? `${value.toFixed(value % 1 ? 1 : 0)}%`
  : '-'
const formatDate = (value?: string) => {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}
const windowSummary = (window?: OllamaCloudUsageWindow) => {
  if (!window) return '-'
  const reset = window.reset_at ? formatDate(window.reset_at) : ''
  return reset
    ? t('admin.accounts.ollamaCloud.windowWithReset', { percent: formatPercent(window.used_percent), reset })
    : formatPercent(window.used_percent)
}

const applyState = (next: OllamaCloudUsageState) => {
  state.value = next
  emit('updated', next)
}

const load = async () => {
  loading.value = true
  try {
    applyState(await adminAPI.accounts.getOllamaCloudUsage(props.account.id))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accounts.ollamaCloud.loadFailed')))
  } finally {
    loading.value = false
  }
}

const setAutoRefresh = async (enabled: boolean) => {
  saving.value = true
  try {
    applyState(await adminAPI.accounts.setOllamaCloudUsageAutoRefresh(props.account.id, enabled))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accounts.ollamaCloud.autoRefreshFailed')))
  } finally {
    saving.value = false
  }
}

const refreshUsage = async () => {
  refreshing.value = true
  try {
    const next = await adminAPI.accounts.refreshOllamaCloudUsage(props.account.id)
    applyState(next)
    if (next.snapshot?.status === 'ok') {
      appStore.showSuccess(t('admin.accounts.ollamaCloud.refreshSuccess'))
    } else {
      const message = formatOllamaSnapshotError(next.snapshot, t)
      appStore.showError(message || t('admin.accounts.ollamaCloud.refreshFailed'))
    }
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(
      error,
      t,
      'admin.accounts.ollamaCloud.errors',
      t('admin.accounts.ollamaCloud.refreshFailed')
    ))
  } finally {
    refreshing.value = false
  }
}

watch(() => props.account.id, () => {
  state.value = props.account.ollama_cloud_usage ?? null
  if (!state.value) void load()
})

onMounted(() => {
  if (!state.value) void load()
})
</script>
