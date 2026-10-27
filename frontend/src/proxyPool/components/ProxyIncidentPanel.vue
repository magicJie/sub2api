<template>
  <!-- 代理池（fork）：落地 IP 故障列表与一键处置。巡检间隔与失败阈值可配置。 -->
  <div class="space-y-3">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <div class="min-w-0">
        <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.proxyPools.incidents') }}</h3>
        <p class="text-xs text-gray-500 dark:text-dark-300">
          {{ t('admin.proxyPools.incidentsHint', { interval: config.interval_seconds, threshold: config.fail_threshold }) }}
        </p>
      </div>
      <div class="flex items-center gap-3">
        <label class="flex items-center gap-1.5 text-xs text-gray-600 dark:text-dark-300">
          <input v-model="showResolved" type="checkbox" class="rounded" />
          {{ t('admin.proxyPools.showResolved') }}
        </label>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="load">
          {{ t('common.refresh') }}
        </button>
      </div>
    </div>

    <form
      class="flex flex-wrap items-end gap-3 rounded-lg border border-gray-200 p-3 dark:border-dark-600"
      @submit.prevent="saveConfig"
    >
      <label class="flex items-center gap-1.5 text-sm">
        <input v-model="configForm.enabled" type="checkbox" class="rounded" />
        {{ t('admin.proxyPools.healthEnabled') }}
      </label>
      <div>
        <label class="input-label" for="egress-interval">{{ t('admin.proxyPools.healthInterval') }}</label>
        <input id="egress-interval" v-model.number="configForm.interval_seconds" type="number" min="30" max="3600" class="input w-28" />
      </div>
      <div>
        <label class="input-label" for="egress-threshold">{{ t('admin.proxyPools.healthThreshold') }}</label>
        <input id="egress-threshold" v-model.number="configForm.fail_threshold" type="number" min="1" max="20" class="input w-24" />
      </div>
      <p class="input-hint flex-1">
        {{ t('admin.proxyPools.healthConfigHint', { minutes: detectMinutes }) }}
      </p>
      <button type="submit" class="btn btn-secondary btn-sm" :disabled="savingConfig || !configDirty">{{ t('common.save') }}</button>
    </form>

    <div v-if="visible.length === 0" class="rounded-lg border border-gray-200 py-6 text-center text-sm text-gray-500 dark:border-dark-600">
      {{ loading ? t('common.loading') : t('admin.proxyPools.noIncidents') }}
    </div>

    <div
      v-for="inc in visible"
      :key="inc.id"
      class="rounded-lg border p-3"
      :class="inc.status === 'open' ? 'border-red-200 bg-red-50/50 dark:border-red-900/50 dark:bg-red-900/10' : 'border-gray-200 dark:border-dark-600'"
    >
      <div class="flex flex-wrap items-start justify-between gap-2">
        <div class="min-w-0">
          <div class="flex flex-wrap items-center gap-2">
            <span class="font-medium text-gray-900 dark:text-white">{{ inc.proxy_name || `#${inc.proxy_id}` }}</span>
            <span class="badge" :class="inc.layer === 'egress' ? 'badge-danger' : 'badge-warning'">{{ layerLabel(inc.layer) }}</span>
            <span class="badge" :class="inc.status === 'open' ? 'badge-danger' : 'badge-success'">{{ statusLabel(inc) }}</span>
          </div>
          <p class="mt-1 break-all text-xs text-gray-500 dark:text-dark-300">
            {{ formatDateTime(inc.first_failed_at) }} · {{ t('admin.proxyPools.failCount', { n: inc.fail_count }) }} · {{ inc.last_error }}
          </p>
        </div>
        <div class="flex shrink-0 gap-2">
          <button type="button" class="btn btn-secondary btn-sm" :disabled="busyKey !== ''" @click="checkNow(inc)">
            {{ t('admin.proxyPools.checkNow') }}
          </button>
          <button
            v-if="inc.layer === 'egress' && inc.accounts.some((a) => a.current_proxy_id === inc.proxy_id)"
            type="button"
            class="btn btn-secondary btn-sm"
            :disabled="busyKey !== ''"
            @click="confirmMigrate = inc"
          >
            {{ t('admin.proxyPools.migrateAll') }}
          </button>
        </div>
      </div>

      <div v-if="inc.accounts.length > 0" class="mt-2 overflow-x-auto">
        <table class="min-w-full text-sm">
          <thead class="text-left text-xs text-gray-500 dark:text-dark-300">
            <tr>
              <th class="py-1 pr-3">{{ t('admin.proxyPools.account') }}</th>
              <th class="py-1 pr-3">{{ t('admin.proxyPools.platform') }}</th>
              <th class="py-1 pr-3">{{ t('admin.proxyPools.action') }}</th>
              <th class="py-1 pr-3"></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="a in inc.accounts" :key="a.account_id" class="border-t border-gray-100 dark:border-dark-700">
              <td class="py-1.5 pr-3">
                <span class="font-medium text-gray-900 dark:text-white">{{ a.account_name || `#${a.account_id}` }}</span>
                <span class="ml-1 text-xs text-gray-400">#{{ a.account_id }}</span>
              </td>
              <td class="py-1.5 pr-3 text-xs">{{ a.platform }}</td>
              <td class="py-1.5 pr-3 text-xs">
                <span class="badge" :class="actionClass(a)">{{ actionLabel(a) }}</span>
                <span v-if="a.message" class="ml-1 text-gray-400" :title="a.message">{{ a.message }}</span>
              </td>
              <td class="whitespace-nowrap py-1.5 text-right">
                <template v-if="a.action === 'paused' && !a.resumed_at">
                  <button type="button" class="text-xs text-primary-600 hover:underline" :disabled="busyKey !== ''" @click="resume(a)">
                    {{ t('admin.proxyPools.resume') }}
                  </button>
                  <button type="button" class="ml-3 text-xs text-primary-600 hover:underline" @click="openReplace(a)">
                    {{ t('admin.proxyPools.replaceIp') }}
                  </button>
                </template>
                <button
                  v-else-if="a.current_proxy_id === inc.proxy_id && inc.status === 'open'"
                  type="button"
                  class="text-xs text-primary-600 hover:underline"
                  @click="openReplace(a)"
                >
                  {{ t('admin.proxyPools.replaceIp') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <ReplaceProxyDialog
      :show="replace.open"
      :account-id="replace.accountId"
      :account-name="replace.accountName"
      @close="replace.open = false"
      @replaced="onChanged"
    />

    <ConfirmDialog
      :show="confirmMigrate !== null"
      :title="t('admin.proxyPools.migrateAll')"
      :message="t('admin.proxyPools.migrateConfirm', { name: confirmMigrate?.proxy_name ?? '' })"
      @confirm="migrate"
      @cancel="confirmMigrate = null"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { formatDateTime } from '@/utils/format'
import { extractI18nErrorMessage } from '@/utils/apiError'
import proxyPoolAPI, {
  type ProxyEgressHealthConfig,
  type ProxyEgressIncident,
  type ProxyEgressIncidentAccount
} from '../api'
import ReplaceProxyDialog from './ReplaceProxyDialog.vue'

const props = withDefaults(defineProps<{ active?: boolean }>(), { active: true })
const emit = defineEmits<{ changed: [] }>()

const { t } = useI18n()
const appStore = useAppStore()

const incidents = ref<ProxyEgressIncident[]>([])
const loading = ref(false)
const showResolved = ref(false)
const busyKey = ref('')
const confirmMigrate = ref<ProxyEgressIncident | null>(null)
const replace = reactive({ open: false, accountId: null as number | null, accountName: '' })

const config = ref<ProxyEgressHealthConfig>({ enabled: true, interval_seconds: 120, fail_threshold: 3 })
const configForm = reactive<ProxyEgressHealthConfig>({ ...config.value })
const savingConfig = ref(false)
const configDirty = computed(
  () =>
    configForm.enabled !== config.value.enabled ||
    configForm.interval_seconds !== config.value.interval_seconds ||
    configForm.fail_threshold !== config.value.fail_threshold
)
// 从 IP 挂掉到处置账号的大致时长
const detectMinutes = computed(() =>
  Math.max(1, Math.round(((Number(configForm.interval_seconds) || 0) * (Number(configForm.fail_threshold) || 0)) / 60))
)

const loadConfig = async () => {
  try {
    config.value = await proxyPoolAPI.getEgressHealthConfig()
    Object.assign(configForm, config.value)
  } catch {
    // 巡检服务不可用时保留默认展示
  }
}

const saveConfig = async () => {
  savingConfig.value = true
  try {
    config.value = await proxyPoolAPI.updateEgressHealthConfig({
      enabled: configForm.enabled,
      interval_seconds: Math.floor(Number(configForm.interval_seconds)),
      fail_threshold: Math.floor(Number(configForm.fail_threshold))
    })
    Object.assign(configForm, config.value)
    appStore.showSuccess(t('admin.proxyPools.healthConfigSaved'))
  } catch (error) {
    appStore.showError(errorText(error))
  } finally {
    savingConfig.value = false
  }
}

const visible = computed(() => incidents.value.filter((i) => showResolved.value || i.status === 'open'))

const errorText = (error: unknown) =>
  extractI18nErrorMessage(error, t, 'admin.proxyPools.errors', t('admin.proxyPools.loadFailed'))

const layerLabel = (layer: string) =>
  layer === 'egress' ? t('admin.proxyPools.layerEgress') : t('admin.proxyPools.layerTransport')
const statusLabel = (inc: ProxyEgressIncident) => {
  if (inc.status === 'resolved') return t('admin.proxyPools.incidentResolved')
  return inc.handled_at ? t('admin.proxyPools.incidentHandled') : t('admin.proxyPools.incidentPending')
}
const actionLabel = (a: ProxyEgressIncidentAccount) => {
  if (a.action === 'paused') return a.resumed_at ? t('admin.proxyPools.actionResumed') : t('admin.proxyPools.actionPaused')
  if (a.action === 'reassigned') return t('admin.proxyPools.actionReassigned')
  return t('admin.proxyPools.actionAlertOnly')
}
const actionClass = (a: ProxyEgressIncidentAccount) => {
  if (a.action === 'paused' && !a.resumed_at) return 'badge-danger'
  if (a.action === 'alert_only') return 'badge-warning'
  return 'badge-success'
}

const load = async () => {
  loading.value = true
  try {
    incidents.value = await proxyPoolAPI.listIncidents()
  } catch (error) {
    appStore.showError(errorText(error))
  } finally {
    loading.value = false
  }
}

watch(
  () => props.active,
  (active) => {
    if (active) void Promise.all([load(), loadConfig()])
  },
  { immediate: true }
)

const onChanged = async () => {
  await load()
  emit('changed')
}

const run = async (key: string, fn: () => Promise<void>) => {
  busyKey.value = key
  try {
    await fn()
    await onChanged()
  } catch (error) {
    appStore.showError(errorText(error))
  } finally {
    busyKey.value = ''
  }
}

const resume = (a: ProxyEgressIncidentAccount) =>
  run(`resume-${a.account_id}`, async () => {
    await proxyPoolAPI.resumeAccountProxy(a.account_id)
    appStore.showSuccess(t('admin.proxyPools.resumed'))
  })

const checkNow = (inc: ProxyEgressIncident) =>
  run(`check-${inc.id}`, async () => {
    const res = await proxyPoolAPI.checkProxyHealth(inc.proxy_id)
    if (res) appStore.showError(t('admin.proxyPools.stillFailing', { error: res.last_error }))
    else appStore.showSuccess(t('admin.proxyPools.checkOk'))
  })

const migrate = () => {
  const inc = confirmMigrate.value
  confirmMigrate.value = null
  if (!inc) return
  return run(`migrate-${inc.id}`, async () => {
    const results = await proxyPoolAPI.migrateProxyAccounts(inc.proxy_id)
    const failed = results.filter((r) => r.error).length
    const msg = t('admin.proxyPools.migrated', { ok: results.length - failed, failed })
    if (failed > 0) appStore.showError(msg)
    else appStore.showSuccess(msg)
  })
}

const openReplace = (a: ProxyEgressIncidentAccount) => {
  replace.accountId = a.account_id
  replace.accountName = a.account_name
  replace.open = true
}
</script>
