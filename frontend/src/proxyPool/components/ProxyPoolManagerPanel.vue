<template>
  <div class="space-y-6">
    <PlatformIPPolicyPanel :policies="policies" :pools="pools" @changed="onPoliciesChanged" />
    <div class="flex min-h-[420px] flex-col gap-4 md:flex-row">
      <!-- 左：代理池列表 -->
      <div class="w-full shrink-0 space-y-2 md:w-60">
        <button type="button" class="btn btn-primary w-full" @click="openPoolForm(null)">
          <Icon name="plus" size="sm" class="mr-1" />
          {{ t('admin.proxyPools.createPool') }}
        </button>
        <div v-if="loadingPools" class="py-6 text-center text-sm text-gray-500">{{ t('common.loading') }}</div>
        <button
          v-for="pool in pools"
          :key="pool.id"
          type="button"
          class="w-full rounded-lg border px-3 py-2 text-left text-sm transition-colors"
          :class="
            selectedPoolId === pool.id
              ? 'border-primary-500 bg-primary-50 dark:bg-primary-900/20'
              : 'border-gray-200 hover:bg-gray-50 dark:border-dark-600 dark:hover:bg-dark-700'
          "
          @click="selectPool(pool.id)"
        >
          <div class="flex items-center justify-between gap-2">
            <span class="truncate font-medium text-gray-900 dark:text-white">{{ pool.name }}</span>
            <span v-if="pool.is_default" class="badge badge-primary shrink-0">{{ t('admin.proxyPools.defaultPool') }}</span>
          </div>
          <div class="mt-0.5 text-xs text-gray-500 dark:text-dark-300">
            {{ t('admin.proxyPools.proxyCount') }} {{ pool.proxy_count }}
          </div>
        </button>
      </div>

      <!-- 右：池详情 -->
      <div class="min-w-0 flex-1 space-y-3">
        <template v-if="selectedPool">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div class="min-w-0">
              <h3 class="truncate text-base font-semibold text-gray-900 dark:text-white">{{ selectedPool.name }}</h3>
              <p v-if="selectedPool.description" class="text-xs text-gray-500 dark:text-dark-300">{{ selectedPool.description }}</p>
            </div>
            <div class="flex gap-2">
              <button type="button" class="btn btn-secondary btn-sm" @click="showAssign = !showAssign">
                {{ t('admin.proxyPools.addProxies') }}
              </button>
              <button type="button" class="btn btn-secondary btn-sm" @click="openPoolForm(selectedPool)">
                {{ t('common.edit') }}
              </button>
              <button
                v-if="!selectedPool.is_default"
                type="button"
                class="btn btn-danger btn-sm"
                @click="confirmDelete = true"
              >
                {{ t('common.delete') }}
              </button>
            </div>
          </div>

          <div v-if="showAssign" class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
            <p class="input-hint mb-2">{{ t('admin.proxyPools.addProxiesHint') }}</p>
            <div class="max-h-48 space-y-1 overflow-y-auto">
              <label
                v-for="p in assignableProxies"
                :key="p.id"
                class="flex cursor-pointer items-center gap-2 rounded px-2 py-1 text-sm hover:bg-gray-50 dark:hover:bg-dark-700"
              >
                <input v-model="assignSelection" type="checkbox" :value="p.id" class="rounded" />
                <span class="truncate">{{ p.name }}</span>
                <span class="text-xs text-gray-500">{{ p.protocol }}://{{ p.host }}:{{ p.port }}</span>
                <span class="ml-auto text-xs text-gray-400">{{ poolName(p.pool_id) }}</span>
              </label>
            </div>
            <div class="mt-2 flex justify-end">
              <button
                type="button"
                class="btn btn-primary btn-sm"
                :disabled="assignSelection.length === 0 || assigning"
                @click="handleAssign"
              >
                {{ t('admin.proxyPools.addProxies') }} ({{ assignSelection.length }})
              </button>
            </div>
          </div>

          <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-600">
            <table class="min-w-full text-sm">
              <thead class="bg-gray-50 text-left text-xs text-gray-500 dark:bg-dark-700 dark:text-dark-300">
                <tr>
                  <th class="px-3 py-2">{{ t('admin.proxies.columns.name') }}</th>
                  <th class="px-3 py-2">{{ t('admin.proxies.columns.address') }}</th>
                  <th class="px-3 py-2">{{ t('admin.proxies.columns.status') }}</th>
                  <th class="px-3 py-2">{{ t('admin.proxyPools.usage') }}</th>
                  <th class="px-3 py-2"></th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="loadingMembers">
                  <td colspan="5" class="px-3 py-6 text-center text-gray-500">{{ t('common.loading') }}</td>
                </tr>
                <tr v-else-if="members.length === 0">
                  <td colspan="5" class="px-3 py-6 text-center text-gray-500">{{ t('admin.proxyPools.noMembers') }}</td>
                </tr>
                <tr
                  v-for="m in members"
                  v-else
                  :key="m.proxy.id"
                  class="border-t border-gray-100 dark:border-dark-700"
                >
                  <td class="px-3 py-2 font-medium text-gray-900 dark:text-white">{{ m.proxy.name }}</td>
                  <td class="px-3 py-2 text-xs text-gray-500">{{ m.proxy.protocol }}://{{ m.proxy.host }}:{{ m.proxy.port }}</td>
                  <td class="px-3 py-2">
                    <span class="badge" :class="m.proxy.status === 'active' ? 'badge-success' : 'badge-gray'">{{ m.proxy.status }}</span>
                  </td>
                  <td class="px-3 py-2">
                    <div class="flex flex-wrap gap-1">
                      <span
                        v-for="platform in memberPlatforms(m)"
                        :key="platform"
                        class="badge"
                        :class="m.retired_platforms.includes(platform) ? 'badge-danger' : 'badge-gray'"
                        :title="m.retired_platforms.includes(platform) ? t('admin.proxyPools.retired') : ''"
                      >
                        {{ platform }} {{ m.history_counts[platform] ?? 0 }}{{ limitSuffix(platform) }} / {{ m.active_counts[platform] ?? 0 }}
                      </span>
                      <span v-if="memberPlatforms(m).length === 0" class="text-xs text-gray-400">-</span>
                    </div>
                  </td>
                  <td class="px-3 py-2 text-right">
                    <button type="button" class="text-xs text-primary-600 hover:underline" @click="openBindings(m.proxy)">
                      {{ t('admin.proxyPools.bindings') }}
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </template>
      </div>
    </div>

    <!-- 新建 / 编辑代理池 -->
    <BaseDialog
      :show="poolForm.open"
      :title="poolForm.id ? t('admin.proxyPools.editPool') : t('admin.proxyPools.createPool')"
      width="normal"
      @close="poolForm.open = false"
    >
      <form id="proxy-pool-form" class="space-y-4" @submit.prevent="handleSavePool">
        <div>
          <label class="input-label">{{ t('admin.proxyPools.name') }}</label>
          <input v-model="poolForm.name" type="text" class="input" required maxlength="100" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxyPools.description') }}</label>
          <input v-model="poolForm.description" type="text" class="input" />
        </div>
      </form>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="poolForm.open = false">{{ t('common.cancel') }}</button>
          <button type="submit" form="proxy-pool-form" class="btn btn-primary" :disabled="savingPool">{{ t('common.save') }}</button>
        </div>
      </template>
    </BaseDialog>

    <!-- 绑定记录 -->
    <BaseDialog
      :show="bindings.open"
      :title="t('admin.proxyPools.bindingsTitle', { name: bindings.proxyName })"
      width="wide"
      @close="bindings.open = false"
    >
      <div v-if="bindings.loading" class="py-6 text-center text-gray-500">{{ t('common.loading') }}</div>
      <template v-else>
        <div class="mb-3 flex flex-wrap gap-2">
          <span v-for="platform in bindings.data?.platforms ?? []" :key="platform" class="badge badge-gray">
            {{ platform }}：{{ t('admin.proxyPools.usage') }} {{ bindings.data?.summary[platform]?.history ?? 0 }} / {{ bindings.data?.summary[platform]?.active ?? 0 }}
          </span>
        </div>
        <div v-if="(bindings.data?.bindings.length ?? 0) === 0" class="py-6 text-center text-gray-500">
          {{ t('admin.proxyPools.noBindings') }}
        </div>
        <div v-else class="max-h-[60vh] overflow-auto rounded-lg border border-gray-200 dark:border-dark-600">
          <table class="min-w-full text-sm">
            <thead class="bg-gray-50 text-left text-xs text-gray-500 dark:bg-dark-700 dark:text-dark-300">
              <tr>
                <th class="px-3 py-2">{{ t('admin.proxyPools.account') }}</th>
                <th class="px-3 py-2">{{ t('admin.proxyPools.platform') }}</th>
                <th class="px-3 py-2">{{ t('admin.proxyPools.source') }}</th>
                <th class="px-3 py-2">{{ t('admin.proxyPools.boundAt') }}</th>
                <th class="px-3 py-2">{{ t('admin.proxyPools.unboundAt') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="b in bindings.data?.bindings ?? []" :key="b.id" class="border-t border-gray-100 dark:border-dark-700">
                <td class="px-3 py-2">
                  <span class="font-medium text-gray-900 dark:text-white">{{ b.account_name || `#${b.account_id}` }}</span>
                  <span class="ml-1 text-xs text-gray-400">#{{ b.account_id }}</span>
                </td>
                <td class="px-3 py-2">{{ b.platform }}</td>
                <td class="px-3 py-2">
                  <span class="badge" :class="b.source === 'pool' ? 'badge-primary' : 'badge-gray'">
                    {{ b.source === 'pool' ? `${t('admin.proxyPools.sourcePool')} ${b.pool_name}` : t('admin.proxyPools.sourceManual') }}
                  </span>
                  <span v-if="b.locked" class="badge badge-warning ml-1">{{ t('admin.proxyPools.locked') }}</span>
                </td>
                <td class="px-3 py-2 text-xs">{{ formatDateTime(b.bound_at) }}</td>
                <td class="px-3 py-2 text-xs">
                  <template v-if="b.unbound_at">
                    {{ formatDateTime(b.unbound_at) }}
                    <span class="text-gray-400">· {{ reasonLabel(b.unbind_reason) }}</span>
                  </template>
                  <span v-else class="badge badge-success">{{ t('admin.proxyPools.active') }}</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="confirmDelete"
      :title="t('common.delete')"
      :message="t('admin.proxyPools.deleteConfirm', { name: selectedPool?.name ?? '' })"
      :danger="true"
      @confirm="handleDeletePool"
      @cancel="confirmDelete = false"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { Proxy } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import PlatformIPPolicyPanel from './PlatformIPPolicyPanel.vue'
import { formatDateTime } from '@/utils/format'
import { extractI18nErrorMessage } from '@/utils/apiError'
import proxyPoolAPI, { type ProxyBindingsResult, type ProxyIPPolicy, type ProxyPool, type ProxyPoolMember } from '../api'

const props = withDefaults(defineProps<{ active?: boolean }>(), { active: true })
const emit = defineEmits<{ changed: [] }>()

const { t } = useI18n()
const appStore = useAppStore()

const pools = ref<ProxyPool[]>([])
const policies = ref<ProxyIPPolicy[]>([])
const loadingPools = ref(false)
const selectedPoolId = ref<number | null>(null)
const members = ref<ProxyPoolMember[]>([])
const loadingMembers = ref(false)
const allProxies = ref<Proxy[]>([])
const showAssign = ref(false)
const assignSelection = ref<number[]>([])
const assigning = ref(false)
const confirmDelete = ref(false)
const savingPool = ref(false)

const poolForm = reactive({
  open: false,
  id: 0,
  name: '',
  description: ''
})

const bindings = reactive({
  open: false,
  loading: false,
  proxyName: '',
  data: null as ProxyBindingsResult | null
})

const selectedPool = computed(() => pools.value.find((p) => p.id === selectedPoolId.value) ?? null)
const defaultPoolId = computed(() => pools.value.find((p) => p.is_default)?.id ?? null)
const memberIds = computed(() => new Set(members.value.map((m) => m.proxy.id)))
// 传输代理（role=transport）不进代理池
const assignableProxies = computed(() =>
  allProxies.value.filter((p) => !memberIds.value.has(p.id) && p.role !== 'transport')
)

const showError = (error: unknown, fallback: string) => {
  appStore.showError(extractI18nErrorMessage(error, t, 'admin.proxyPools.errors', fallback))
}

const poolName = (poolId?: number | null) => {
  const id = poolId ?? defaultPoolId.value
  return pools.value.find((p) => p.id === id)?.name ?? ''
}

// 只展示能从当前池取 IP 的平台策略与有历史记录的平台
const poolPolicies = computed(() =>
  policies.value.filter((p) => p.pool_ids.length === 0 || (selectedPoolId.value !== null && p.pool_ids.includes(selectedPoolId.value)))
)

const limitSuffix = (platform: string) => {
  const limit = policies.value.find((p) => p.platform === platform)?.max_lifetime_accounts
  return limit ? `/${limit}` : ''
}

const memberPlatforms = (m: ProxyPoolMember) => {
  const set = new Set([...Object.keys(m.history_counts ?? {}), ...poolPolicies.value.map((p) => p.platform)])
  return [...set].sort()
}

const reasonLabel = (reason: string) => {
  if (reason === 'proxy_changed') return t('admin.proxyPools.reasonProxyChanged')
  if (reason === 'account_deleted') return t('admin.proxyPools.reasonAccountDeleted')
  return reason
}

const loadPools = async () => {
  loadingPools.value = true
  try {
    pools.value = await proxyPoolAPI.listPools()
    if (!pools.value.some((p) => p.id === selectedPoolId.value)) {
      selectedPoolId.value = pools.value[0]?.id ?? null
    }
  } catch (error) {
    showError(error, t('admin.proxyPools.loadFailed'))
  } finally {
    loadingPools.value = false
  }
}

const loadMembers = async () => {
  if (!selectedPoolId.value) {
    members.value = []
    return
  }
  loadingMembers.value = true
  try {
    const res = await proxyPoolAPI.listMembers(selectedPoolId.value)
    members.value = res.members ?? []
  } catch (error) {
    showError(error, t('admin.proxyPools.loadFailed'))
  } finally {
    loadingMembers.value = false
  }
}

const loadAllProxies = async () => {
  try {
    allProxies.value = await adminAPI.proxies.getAll()
  } catch {
    allProxies.value = []
  }
}

const selectPool = (id: number) => {
  selectedPoolId.value = id
  showAssign.value = false
  assignSelection.value = []
}

const loadPolicies = async () => {
  try {
    policies.value = await proxyPoolAPI.listPolicies()
  } catch (error) {
    showError(error, t('admin.proxyPools.loadFailed'))
  }
}

const onPoliciesChanged = async () => {
  await Promise.all([loadPolicies(), loadMembers()])
  emit('changed')
}

const reload = async () => {
  await Promise.all([loadPools(), loadAllProxies(), loadPolicies()])
  await loadMembers()
}

watch(
  () => props.active,
  (active) => {
    if (active) void reload()
  },
  { immediate: true }
)
watch(selectedPoolId, () => void loadMembers())

const openPoolForm = (pool: ProxyPool | null) => {
  poolForm.id = pool?.id ?? 0
  poolForm.name = pool?.name ?? ''
  poolForm.description = pool?.description ?? ''
  poolForm.open = true
}

const handleSavePool = async () => {
  const payload = { name: poolForm.name.trim(), description: poolForm.description.trim() }
  savingPool.value = true
  try {
    const saved = poolForm.id
      ? await proxyPoolAPI.updatePool(poolForm.id, payload)
      : await proxyPoolAPI.createPool(payload)
    poolForm.open = false
    selectedPoolId.value = saved.id
    appStore.showSuccess(t('admin.proxyPools.saved'))
    await loadPools()
    await loadMembers()
    emit('changed')
  } catch (error) {
    showError(error, t('admin.proxyPools.loadFailed'))
  } finally {
    savingPool.value = false
  }
}

const handleDeletePool = async () => {
  confirmDelete.value = false
  if (!selectedPool.value) return
  try {
    await proxyPoolAPI.deletePool(selectedPool.value.id)
    appStore.showSuccess(t('admin.proxyPools.deleted'))
    selectedPoolId.value = null
    await reload()
    emit('changed')
  } catch (error) {
    showError(error, t('admin.proxyPools.loadFailed'))
  }
}

const handleAssign = async () => {
  if (!selectedPoolId.value || assignSelection.value.length === 0) return
  assigning.value = true
  try {
    await proxyPoolAPI.assignProxies(selectedPoolId.value, assignSelection.value)
    appStore.showSuccess(t('admin.proxyPools.assigned'))
    assignSelection.value = []
    showAssign.value = false
    await reload()
    emit('changed')
  } catch (error) {
    showError(error, t('admin.proxyPools.loadFailed'))
  } finally {
    assigning.value = false
  }
}

const openBindings = async (proxy: Proxy) => {
  bindings.open = true
  bindings.loading = true
  bindings.proxyName = proxy.name
  bindings.data = null
  try {
    bindings.data = await proxyPoolAPI.getProxyBindings(proxy.id)
  } catch (error) {
    showError(error, t('admin.proxyPools.loadFailed'))
  } finally {
    bindings.loading = false
  }
}
</script>
