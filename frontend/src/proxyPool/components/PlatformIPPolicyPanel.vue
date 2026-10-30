<template>
  <!-- 代理池（fork）：平台 IP 策略与分组覆盖。分组策略优先；都没有的账号不能从代理池分配。 -->
  <div class="space-y-3">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <div class="min-w-0">
        <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.proxyPools.policies') }}</h3>
        <p class="text-xs text-gray-500 dark:text-dark-300">{{ t('admin.proxyPools.policiesHint') }}</p>
      </div>
      <button type="button" class="btn btn-secondary btn-sm" @click="openForm(null)">
        <Icon name="plus" size="sm" class="mr-1" />
        {{ t('admin.proxyPools.addPolicy') }}
      </button>
    </div>

    <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-600">
      <table class="min-w-full text-sm">
        <thead class="bg-gray-50 text-left text-xs text-gray-500 dark:bg-dark-700 dark:text-dark-300">
          <tr>
            <th class="px-3 py-2">{{ t('admin.proxyPools.platform') }}</th>
            <th class="px-3 py-2">{{ t('admin.proxyPools.policyMode') }}</th>
            <th class="px-3 py-2">{{ t('admin.proxyPools.policyPools') }}</th>
            <th class="px-3 py-2">{{ t('admin.proxyPools.maxLifetime') }}</th>
            <th class="px-3 py-2">{{ t('admin.proxyPools.maxActive') }}</th>
            <th class="px-3 py-2">{{ t('admin.proxyPools.onFailure') }}</th>
            <th class="px-3 py-2"></th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="rows.length === 0">
            <td colspan="7" class="px-3 py-6 text-center text-gray-500">{{ t('admin.proxyPools.noPolicies') }}</td>
          </tr>
          <tr v-for="p in rows" :key="rowKey(p)" class="border-t border-gray-100 dark:border-dark-700">
            <td class="px-3 py-2 font-medium text-gray-900 dark:text-white">
              <template v-if="p.group_id">
                <span class="pl-4 text-gray-400">↳</span>
                {{ p.group_name }}
                <span class="badge badge-gray ml-1">{{ t('admin.proxyPools.groupOverride') }}</span>
              </template>
              <template v-else>{{ p.platform }}</template>
            </td>
            <td class="px-3 py-2">
              <span class="badge" :class="p.mode === 'churn' ? 'badge-warning' : 'badge-primary'">{{ modeLabel(p.mode) }}</span>
            </td>
            <td class="px-3 py-2 text-xs">{{ poolsLabel(p.pool_ids) }}</td>
            <td class="px-3 py-2">{{ p.max_lifetime_accounts ?? t('admin.proxyPools.noLimits') }}</td>
            <td class="px-3 py-2">{{ p.max_active_accounts ?? t('admin.proxyPools.noLimits') }}</td>
            <td class="px-3 py-2 text-xs">{{ failureLabel(p.on_egress_failure) }}</td>
            <td class="whitespace-nowrap px-3 py-2 text-right">
              <button type="button" class="text-xs text-primary-600 hover:underline" @click="openForm(p)">{{ t('common.edit') }}</button>
              <button type="button" class="ml-3 text-xs text-red-600 hover:underline" @click="pendingDelete = p">
                {{ t('common.delete') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <BaseDialog
      :show="form.open"
      :title="form.editing ? t('admin.proxyPools.editPolicy') : t('admin.proxyPools.addPolicy')"
      width="normal"
      @close="form.open = false"
    >
      <form id="proxy-ip-policy-form" class="space-y-4" @submit.prevent="handleSave">
        <div>
          <label class="input-label" for="policy-scope">{{ t('admin.proxyPools.policyScope') }}</label>
          <Select id="policy-scope" v-model="form.scope" :options="scopeOptions" :disabled="form.editing" />
        </div>
        <div v-if="form.scope === 'platform'">
          <label class="input-label" for="policy-platform">{{ t('admin.proxyPools.platform') }}</label>
          <Select id="policy-platform" v-model="form.platform" :options="platformOptions" :disabled="form.editing" />
        </div>
        <div v-else>
          <label class="input-label" for="policy-group">{{ t('admin.proxyPools.policyGroup') }}</label>
          <Select id="policy-group" v-model="form.group_id" :options="groupOptions" :disabled="form.editing" searchable />
          <p class="input-hint mt-1">{{ t('admin.proxyPools.groupOverrideHint') }}</p>
        </div>
        <div>
          <label class="input-label" for="policy-mode">{{ t('admin.proxyPools.policyMode') }}</label>
          <Select id="policy-mode" v-model="form.mode" :options="modeOptions" />
          <p class="input-hint mt-1">{{ form.mode === 'churn' ? t('admin.proxyPools.modeChurnHint') : t('admin.proxyPools.modeStickyHint') }}</p>
        </div>
        <fieldset>
          <legend class="input-label">{{ t('admin.proxyPools.policyPools') }}</legend>
          <p class="input-hint mb-2">{{ t('admin.proxyPools.policyPoolsHint') }}</p>
          <div class="flex flex-wrap gap-3">
            <label v-for="pool in pools" :key="pool.id" class="flex items-center gap-1.5 text-sm">
              <input v-model="form.pool_ids" type="checkbox" :value="pool.id" class="rounded" />
              {{ pool.name }}
            </label>
          </div>
        </fieldset>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="input-label" for="policy-lifetime">{{ t('admin.proxyPools.maxLifetime') }}</label>
            <input
              id="policy-lifetime"
              v-model.number="form.max_lifetime_accounts"
              type="number"
              min="1"
              class="input"
              :placeholder="t('admin.proxyPools.noLimits')"
            />
          </div>
          <div>
            <label class="input-label" for="policy-active">{{ t('admin.proxyPools.maxActive') }}</label>
            <input
              id="policy-active"
              v-model.number="form.max_active_accounts"
              type="number"
              min="1"
              class="input"
              :placeholder="t('admin.proxyPools.noLimits')"
            />
          </div>
        </div>
        <p class="input-hint">{{ t('admin.proxyPools.limitsHint') }}</p>
        <div v-if="form.mode === 'churn'">
          <label class="input-label" for="policy-failure">{{ t('admin.proxyPools.onFailure') }}</label>
          <Select id="policy-failure" v-model="form.on_egress_failure" :options="failureOptions" />
        </div>
        <p v-else class="input-hint">{{ t('admin.proxyPools.stickyFailureHint') }}</p>
      </form>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="form.open = false">{{ t('common.cancel') }}</button>
          <button type="submit" form="proxy-ip-policy-form" class="btn btn-primary" :disabled="saving || !canSave">
            {{ t('common.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="pendingDelete !== null"
      :title="t('common.delete')"
      :message="deleteMessage"
      :danger="true"
      @confirm="handleDelete"
      @cancel="pendingDelete = null"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'
import { adminAPI } from '@/api/admin'
import type { AdminGroup } from '@/types'
import { extractI18nErrorMessage } from '@/utils/apiError'
import proxyPoolAPI, { type ProxyIPPolicy, type ProxyIPPolicyMode, type ProxyPool } from '../api'

const props = defineProps<{ policies: ProxyIPPolicy[]; groupPolicies: ProxyIPPolicy[]; pools: ProxyPool[] }>()
const emit = defineEmits<{ changed: [] }>()

const { t } = useI18n()
const appStore = useAppStore()

const saving = ref(false)
const pendingDelete = ref<ProxyIPPolicy | null>(null)
const groups = ref<AdminGroup[]>([])
const form = reactive({
  open: false,
  editing: false,
  scope: 'platform' as 'platform' | 'group',
  platform: '',
  group_id: null as number | null,
  mode: 'sticky' as ProxyIPPolicyMode,
  pool_ids: [] as number[],
  max_lifetime_accounts: null as number | '' | null,
  max_active_accounts: null as number | '' | null,
  on_egress_failure: 'reassign' as 'pause' | 'reassign'
})

// 平台策略在前，各自的分组覆盖紧随其后；没有平台策略的分组覆盖排在最后。
const rows = computed(() => {
  const out: ProxyIPPolicy[] = []
  const byPlatform = (platform: string) => props.groupPolicies.filter((g) => g.platform === platform)
  for (const p of props.policies) out.push(p, ...byPlatform(p.platform))
  const covered = new Set(props.policies.map((p) => p.platform))
  out.push(...props.groupPolicies.filter((g) => !covered.has(g.platform)))
  return out
})
const rowKey = (p: ProxyIPPolicy) => (p.group_id ? `g:${p.group_id}` : `p:${p.platform}`)

const scopeOptions = computed(() => [
  { value: 'platform', label: t('admin.proxyPools.scopePlatform') },
  { value: 'group', label: t('admin.proxyPools.scopeGroup') }
])
const groupOptions = computed(() => {
  const configured = new Set(props.groupPolicies.map((p) => p.group_id))
  return groups.value
    .filter((g) => g.platform !== 'composite' && (form.editing || !configured.has(g.id)))
    .map((g) => ({ value: g.id, label: `${g.name}（${g.platform}）` }))
})
const canSave = computed(() => (form.scope === 'platform' ? !!form.platform : !!form.group_id))
const deleteMessage = computed(() => {
  const p = pendingDelete.value
  if (!p) return ''
  return p.group_id
    ? t('admin.proxyPools.deleteGroupPolicyConfirm', { group: p.group_name ?? `#${p.group_id}` })
    : t('admin.proxyPools.deletePolicyConfirm', { platform: p.platform })
})

const platformOptions = computed(() => {
  const configured = new Set(props.policies.map((p) => p.platform))
  const opts = CONCRETE_PLATFORM_OPTIONS.filter((o) => form.editing || !configured.has(o.value)).map((o) => ({
    value: o.value as string,
    label: o.label
  }))
  // 编辑已有策略时平台可能不在内置列表中
  if (form.platform && !opts.some((o) => o.value === form.platform)) opts.unshift({ value: form.platform, label: form.platform })
  return opts
})
const modeOptions = computed(() => [
  { value: 'churn', label: t('admin.proxyPools.modeChurn') },
  { value: 'sticky', label: t('admin.proxyPools.modeSticky') }
])
const failureOptions = computed(() => [
  { value: 'reassign', label: t('admin.proxyPools.failureReassign') },
  { value: 'pause', label: t('admin.proxyPools.failurePause') }
])

const modeLabel = (mode: ProxyIPPolicyMode) =>
  mode === 'churn' ? t('admin.proxyPools.modeChurn') : t('admin.proxyPools.modeSticky')
const failureLabel = (v: string) =>
  v === 'reassign' ? t('admin.proxyPools.failureReassign') : t('admin.proxyPools.failurePause')
const poolsLabel = (ids: number[]) => {
  if (ids.length === 0) return t('admin.proxyPools.allPools')
  return ids.map((id) => props.pools.find((p) => p.id === id)?.name ?? `#${id}`).join('、')
}

const toLimit = (v: number | '' | null) => (typeof v === 'number' && v >= 1 ? Math.floor(v) : null)

const openForm = (p: ProxyIPPolicy | null) => {
  form.editing = !!p
  form.scope = p?.group_id ? 'group' : 'platform'
  form.platform = p?.group_id ? '' : (p?.platform ?? '')
  form.group_id = p?.group_id ?? null
  form.mode = p?.mode ?? 'sticky'
  form.pool_ids = [...(p?.pool_ids ?? [])]
  form.max_lifetime_accounts = p?.max_lifetime_accounts ?? null
  form.max_active_accounts = p?.max_active_accounts ?? null
  form.on_egress_failure = p?.on_egress_failure ?? 'reassign'
  form.open = true
}

const showError = (error: unknown) => {
  appStore.showError(extractI18nErrorMessage(error, t, 'admin.proxyPools.errors', t('admin.proxyPools.loadFailed')))
}

const handleSave = async () => {
  if (!canSave.value) return
  saving.value = true
  try {
    const payload = {
      mode: form.mode,
      pool_ids: form.pool_ids,
      max_lifetime_accounts: toLimit(form.max_lifetime_accounts),
      max_active_accounts: toLimit(form.max_active_accounts),
      on_egress_failure: form.mode === 'sticky' ? ('pause' as const) : form.on_egress_failure
    }
    if (form.scope === 'group' && form.group_id) await proxyPoolAPI.upsertGroupPolicy(form.group_id, payload)
    else await proxyPoolAPI.upsertPolicy(form.platform, payload)
    form.open = false
    appStore.showSuccess(t('admin.proxyPools.policySaved'))
    emit('changed')
  } catch (error) {
    showError(error)
  } finally {
    saving.value = false
  }
}

const handleDelete = async () => {
  const p = pendingDelete.value
  pendingDelete.value = null
  if (!p) return
  try {
    if (p.group_id) await proxyPoolAPI.deleteGroupPolicy(p.group_id)
    else await proxyPoolAPI.deletePolicy(p.platform)
    appStore.showSuccess(t('admin.proxyPools.policyDeleted'))
    emit('changed')
  } catch (error) {
    showError(error)
  }
}

onMounted(async () => {
  try {
    groups.value = await adminAPI.groups.getAll()
  } catch (error) {
    showError(error)
  }
})
</script>
