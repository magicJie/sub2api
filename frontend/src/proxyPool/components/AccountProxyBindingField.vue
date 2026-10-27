<template>
  <!-- 代理池（fork）：账号编辑页的代理绑定状态。锁定时只读并提供“解绑”；未锁定时可从代理池分配。 -->
  <div v-if="binding?.locked" class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm dark:border-amber-800/50 dark:bg-amber-900/20">
    <div class="flex items-center justify-between gap-2">
      <div class="flex min-w-0 items-center gap-2">
        <Icon name="lock" size="sm" class="shrink-0 text-amber-600" />
        <span class="truncate font-medium text-gray-900 dark:text-white">{{ binding.proxy_name || `#${binding.proxy_id}` }}</span>
      </div>
      <div class="flex shrink-0 gap-2">
        <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="showReplace = true">
          {{ t('admin.proxyPools.replaceIp') }}
        </button>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="confirmUnbind = true">
          {{ t('admin.proxyPools.unbind') }}
        </button>
      </div>
    </div>
    <p class="input-hint mt-1">{{ t('admin.proxyPools.lockedHint', { pool: binding.pool_name }) }}</p>
  </div>
  <div v-else-if="accountId" class="flex items-center gap-2">
    <div class="flex-1">
      <Select v-model="assignPoolId" :options="poolOptions" :placeholder="t('admin.proxyPools.assignFromPool')" />
    </div>
    <button type="button" class="btn btn-secondary btn-sm" :disabled="!assignPoolId || busy" @click="handleAssign">
      {{ t('admin.proxyPools.assignFromPool') }}
    </button>
  </div>

  <ReplaceProxyDialog
    :show="showReplace"
    :account-id="accountId ?? null"
    @close="showReplace = false"
    @replaced="onReplaced"
  />

  <ConfirmDialog
    :show="confirmUnbind"
    :title="t('admin.proxyPools.unbind')"
    :message="t('admin.proxyPools.unbindConfirm')"
    @confirm="handleUnbind"
    @cancel="confirmUnbind = false"
  />
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import { extractI18nErrorMessage } from '@/utils/apiError'
import proxyPoolAPI, { type ProxyAccountBinding } from '../api'
import ReplaceProxyDialog from './ReplaceProxyDialog.vue'
import { useProxyPoolOptions } from '../useProxyPools'

const props = defineProps<{ accountId: number | null | undefined }>()
const emit = defineEmits<{
  /** 锁定状态变化，父组件据此禁用代理选择 */
  'update:locked': [locked: boolean]
  /** 代理被分配/变化，父组件同步 proxy_id */
  'proxy-changed': [proxyId: number]
}>()

const { t } = useI18n()
const appStore = useAppStore()
const { pools, loadPools } = useProxyPoolOptions()

const binding = ref<ProxyAccountBinding | null>(null)
const assignPoolId = ref<number | null>(null)
const busy = ref(false)
const confirmUnbind = ref(false)
const showReplace = ref(false)

const onReplaced = async (proxyId: number) => {
  emit('proxy-changed', proxyId)
  await load()
}

const poolOptions = computed(() => pools.value.map((p) => ({ value: p.id, label: p.name })))

const load = async () => {
  binding.value = null
  assignPoolId.value = null
  emit('update:locked', false)
  if (!props.accountId) return
  try {
    const [b] = await Promise.all([proxyPoolAPI.getAccountBinding(props.accountId), loadPools()])
    binding.value = b
    emit('update:locked', !!b?.locked)
  } catch {
    binding.value = null
  }
}

watch(() => props.accountId, () => void load(), { immediate: true })

const handleAssign = async () => {
  if (!props.accountId || !assignPoolId.value) return
  busy.value = true
  try {
    const proxy = await proxyPoolAPI.assignAccountFromPool(props.accountId, assignPoolId.value)
    appStore.showSuccess(t('admin.proxyPools.assignedFromPool', { name: proxy.name }))
    emit('proxy-changed', proxy.id)
    await load()
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, 'admin.proxyPools.errors', t('admin.proxyPools.loadFailed')))
  } finally {
    busy.value = false
  }
}

const handleUnbind = async () => {
  confirmUnbind.value = false
  if (!props.accountId) return
  busy.value = true
  try {
    await proxyPoolAPI.unbindAccountProxy(props.accountId)
    appStore.showSuccess(t('admin.proxyPools.unbound'))
    await load()
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, 'admin.proxyPools.errors', t('admin.proxyPools.loadFailed')))
  } finally {
    busy.value = false
  }
}
</script>
