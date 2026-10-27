<template>
  <!-- 代理池（fork）：给账号换落地 IP。候选按平台策略排序，与当前 IP 同国家的优先。 -->
  <BaseDialog :show="show" :title="t('admin.proxyPools.replaceTitle', { name: accountName || `#${accountId}` })" width="wide" @close="emit('close')">
    <p class="input-hint mb-3">{{ t('admin.proxyPools.replaceHint') }}</p>
    <div v-if="loading" class="py-6 text-center text-gray-500">{{ t('common.loading') }}</div>
    <div v-else-if="candidates.length === 0" class="py-6 text-center text-gray-500">{{ emptyText || t('admin.proxyPools.noCandidates') }}</div>
    <div v-else class="max-h-[50vh] overflow-auto rounded-lg border border-gray-200 dark:border-dark-600">
      <table class="min-w-full text-sm">
        <thead class="bg-gray-50 text-left text-xs text-gray-500 dark:bg-dark-700 dark:text-dark-300">
          <tr>
            <th class="px-3 py-2"><span class="sr-only">{{ t('admin.proxyPools.select') }}</span></th>
            <th class="px-3 py-2">{{ t('admin.proxies.columns.name') }}</th>
            <th class="px-3 py-2">{{ t('admin.proxies.columns.address') }}</th>
            <th class="px-3 py-2">{{ t('admin.proxyPools.pool') }}</th>
            <th class="px-3 py-2">{{ t('admin.proxyPools.country') }}</th>
            <th class="px-3 py-2">{{ t('admin.proxyPools.usage') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="c in candidates"
            :key="c.proxy.id"
            class="cursor-pointer border-t border-gray-100 dark:border-dark-700"
            :class="selected === c.proxy.id ? 'bg-primary-50 dark:bg-primary-900/20' : 'hover:bg-gray-50 dark:hover:bg-dark-700'"
            @click="selected = c.proxy.id"
          >
            <td class="px-3 py-2">
              <input
                v-model="selected"
                type="radio"
                name="replace-proxy"
                :value="c.proxy.id"
                :aria-label="c.proxy.name"
              />
            </td>
            <td class="px-3 py-2 font-medium text-gray-900 dark:text-white">{{ c.proxy.name }}</td>
            <td class="px-3 py-2 text-xs text-gray-500">{{ c.proxy.protocol }}://{{ c.proxy.host }}:{{ c.proxy.port }}</td>
            <td class="px-3 py-2 text-xs">{{ c.pool_name }}</td>
            <td class="px-3 py-2 text-xs">
              {{ c.country || '-' }}
              <span v-if="c.same_country" class="badge badge-success ml-1">{{ t('admin.proxyPools.sameCountry') }}</span>
            </td>
            <td class="px-3 py-2">
              <div class="flex flex-wrap gap-1">
                <span v-for="(n, platform) in c.history_counts" :key="platform" class="badge badge-gray">
                  {{ platform }} {{ n }} / {{ c.active_counts[platform] ?? 0 }}
                </span>
                <span v-if="Object.keys(c.history_counts).length === 0" class="text-xs text-gray-400">-</span>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.cancel') }}</button>
        <button type="button" class="btn btn-primary" :disabled="!selected || busy" @click="handleReplace">
          {{ t('admin.proxyPools.replaceConfirm') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { extractI18nErrorMessage } from '@/utils/apiError'
import proxyPoolAPI, { type ProxyReplaceCandidate } from '../api'

const props = defineProps<{ show: boolean; accountId: number | null; accountName?: string }>()
const emit = defineEmits<{ close: []; replaced: [proxyId: number] }>()

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const busy = ref(false)
const candidates = ref<ProxyReplaceCandidate[]>([])
const selected = ref<number | null>(null)
const emptyText = ref('')

const errorText = (error: unknown) =>
  extractI18nErrorMessage(error, t, 'admin.proxyPools.errors', t('admin.proxyPools.loadFailed'))

watch(
  () => [props.show, props.accountId] as const,
  async ([show, accountId]) => {
    if (!show || !accountId) return
    loading.value = true
    candidates.value = []
    selected.value = null
    emptyText.value = ''
    try {
      candidates.value = await proxyPoolAPI.getReplaceCandidates(accountId)
      selected.value = candidates.value[0]?.proxy.id ?? null
    } catch (error) {
      emptyText.value = errorText(error)
    } finally {
      loading.value = false
    }
  },
  { immediate: true }
)

const handleReplace = async () => {
  if (!props.accountId || !selected.value) return
  busy.value = true
  try {
    const proxy = await proxyPoolAPI.replaceAccountProxy(props.accountId, selected.value)
    appStore.showSuccess(t('admin.proxyPools.replaced', { name: proxy.name }))
    emit('replaced', proxy.id)
    emit('close')
  } catch (error) {
    appStore.showError(errorText(error))
  } finally {
    busy.value = false
  }
}
</script>
