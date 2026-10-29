<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between gap-3">
      <p class="text-sm text-gray-500">{{ t('admin.proxyUpstream.endpointHint') }}</p>
      <button type="button" class="btn btn-primary btn-sm shrink-0" @click="startCreate">
        <Icon name="plus" size="sm" class="mr-1" />
        {{ t('admin.proxyUpstream.createEndpoint') }}
      </button>
    </div>

    <form
      v-if="editing"
      class="space-y-4 rounded-lg border border-gray-200 p-4 dark:border-dark-600"
      @submit.prevent="save"
    >
      <h3 class="text-base font-semibold">
        {{ editingId ? t('admin.proxyUpstream.editEndpoint') : t('admin.proxyUpstream.createEndpoint') }}
      </h3>
      <div class="grid gap-4 sm:grid-cols-2">
        <label class="block">
          <span class="input-label">{{ t('admin.proxyUpstream.name') }}</span>
          <input v-model="form.name" data-test="endpoint-name" class="input w-full" required maxlength="255" />
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.proxyUpstream.protocol') }}</span>
          <select v-model="form.protocol" data-test="endpoint-protocol" class="input w-full">
            <option v-for="p in protocols" :key="p" :value="p">{{ p.toUpperCase() }}</option>
          </select>
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.proxyUpstream.host') }}</span>
          <input v-model="form.host" data-test="endpoint-host" class="input w-full" required maxlength="255" />
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.proxyUpstream.port') }}</span>
          <input
            v-model.number="form.port"
            data-test="endpoint-port"
            type="number"
            min="1"
            max="65535"
            step="1"
            class="input w-full"
            required
          />
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.proxyUpstream.username') }}</span>
          <input v-model="form.username" class="input w-full" autocomplete="off" />
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.proxyUpstream.password') }}</span>
          <input
            v-model="form.password"
            data-test="endpoint-password"
            type="password"
            class="input w-full"
            autocomplete="new-password"
            :placeholder="editingHasPassword ? t('admin.proxyUpstream.passwordKeep') : ''"
          />
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.proxyUpstream.status') }}</span>
          <select v-model="form.status" class="input w-full">
            <option value="active">{{ t('admin.proxyUpstream.active') }}</option>
            <option value="inactive">{{ t('admin.proxyUpstream.inactive') }}</option>
          </select>
        </label>
      </div>
      <p v-if="editingId" class="input-hint">{{ t('admin.proxyUpstream.passwordHint') }}</p>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="editing = false">{{ t('common.cancel') }}</button>
        <button type="submit" class="btn btn-primary" :disabled="saving">{{ t('common.save') }}</button>
      </div>
    </form>

    <p v-if="endpoints.length === 0" class="py-4 text-sm text-gray-500">{{ t('admin.proxyUpstream.noEndpoints') }}</p>
    <div v-else class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-600">
      <table class="w-full text-left text-sm">
        <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-700">
          <tr>
            <th class="p-3">{{ t('admin.proxyUpstream.name') }}</th>
            <th class="p-3">{{ t('admin.proxyUpstream.address') }}</th>
            <th class="p-3">{{ t('admin.proxyUpstream.status') }}</th>
            <th class="p-3">{{ t('admin.proxyUpstream.health') }}</th>
            <th class="p-3">{{ t('admin.proxyUpstream.usedBy') }}</th>
            <th class="p-3"></th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="endpoint in endpoints"
            :key="endpoint.id"
            data-test="endpoint-row"
            class="border-t border-gray-100 align-top dark:border-dark-700"
          >
            <td class="p-3 font-medium text-gray-900 dark:text-white">{{ endpoint.name }}</td>
            <td class="p-3">
              <code class="code text-xs">{{ endpoint.protocol }}://{{ endpoint.host }}:{{ endpoint.port }}</code>
              <div v-if="endpoint.username || endpoint.has_password" class="mt-0.5 text-xs text-gray-500">
                {{ endpoint.username || '—' }}{{ endpoint.has_password ? ' · ••••••' : '' }}
              </div>
            </td>
            <td class="p-3">
              <span class="badge" :class="endpoint.status === 'active' ? 'badge-success' : 'badge-gray'">
                {{ endpoint.status === 'active' ? t('admin.proxyUpstream.active') : t('admin.proxyUpstream.inactive') }}
              </span>
            </td>
            <td class="p-3">
              <UpstreamHealthBadge :health="endpoint.health" />
              <div v-if="endpoint.health.state === 'healthy'" class="mt-0.5 text-xs text-gray-500">
                {{ endpoint.health.latency_ms }} ms
              </div>
              <div v-else-if="endpoint.health.consecutive_failures > 0" class="mt-0.5 text-xs text-gray-500">
                {{ t('admin.proxyUpstream.failCount', { n: endpoint.health.consecutive_failures }) }}
              </div>
              <div v-if="endpoint.health.last_checked_at" class="mt-0.5 text-xs text-gray-500">
                {{ t('admin.proxyUpstream.lastChecked') }}: {{ formatDateTime(endpoint.health.last_checked_at) }}
              </div>
              <div
                v-if="endpoint.health.last_error"
                class="mt-0.5 max-w-[240px] truncate text-xs text-amber-600"
                :title="endpoint.health.last_error"
              >
                {{ endpoint.health.last_error }}
              </div>
            </td>
            <td class="p-3 text-xs text-gray-500">{{ endpoint.group_names.join(', ') || '—' }}</td>
            <td class="whitespace-nowrap p-3 text-right">
              <button type="button" class="btn btn-secondary btn-sm" @click="startEdit(endpoint)">
                {{ t('common.edit') }}
              </button>
              <button type="button" class="btn btn-danger btn-sm ml-2" @click="deleting = endpoint">
                {{ t('common.delete') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <ConfirmDialog
      :show="deleting !== null"
      :title="t('admin.proxyUpstream.deleteEndpoint')"
      :message="t('admin.proxyUpstream.deleteEndpointConfirm', { name: deleting?.name ?? '' })"
      danger
      @confirm="remove"
      @cancel="deleting = null"
    />
  </div>
</template>

<script setup lang="ts">
// 前置组（fork）：前置端点（外部 AIO 的 listener）增删改与健康状态
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import { extractI18nErrorMessage } from '@/utils/apiError'
import {
  upstreamEndpointsAPI,
  type UpstreamEndpoint,
  type UpstreamEndpointInput,
  type UpstreamEndpointProtocol
} from './api'
import UpstreamHealthBadge from './UpstreamHealthBadge.vue'
import { UPSTREAM_ERROR_NAMESPACE } from './useUpstreamGroups'

const props = defineProps<{ endpoints: UpstreamEndpoint[] }>()
const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const appStore = useAppStore()
const protocols: UpstreamEndpointProtocol[] = ['socks5', 'socks5h', 'http', 'https']
const defaults = (): Required<UpstreamEndpointInput> => ({
  name: '',
  protocol: 'socks5',
  host: '',
  port: 7890,
  username: '',
  password: '',
  status: 'active'
})
const form = reactive(defaults())
const editing = ref(false)
const editingId = ref<number | null>(null)
const saving = ref(false)
const deleting = ref<UpstreamEndpoint | null>(null)
const editingHasPassword = computed(
  () => props.endpoints.find((e) => e.id === editingId.value)?.has_password ?? false
)

function startCreate() {
  Object.assign(form, defaults())
  editingId.value = null
  editing.value = true
}
function startEdit(endpoint: UpstreamEndpoint) {
  Object.assign(form, {
    name: endpoint.name,
    protocol: endpoint.protocol,
    host: endpoint.host,
    port: endpoint.port,
    username: endpoint.username,
    password: '',
    status: endpoint.status
  })
  editingId.value = endpoint.id
  editing.value = true
}
async function save() {
  saving.value = true
  const input: UpstreamEndpointInput = {
    name: form.name.trim(),
    protocol: form.protocol,
    host: form.host.trim(),
    port: form.port,
    username: form.username.trim(),
    status: form.status
  }
  // 更新时留空即保持原密码，不发送 password
  if (form.password) input.password = form.password
  try {
    if (editingId.value) await upstreamEndpointsAPI.update(editingId.value, input)
    else await upstreamEndpointsAPI.create(input)
    form.password = ''
    editing.value = false
    appStore.showSuccess(t('admin.proxyUpstream.endpointSaved'))
    emit('changed')
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, UPSTREAM_ERROR_NAMESPACE, t('admin.proxyUpstream.saveFailed')))
  } finally {
    saving.value = false
  }
}
async function remove() {
  const target = deleting.value
  if (!target) return
  deleting.value = null
  try {
    await upstreamEndpointsAPI.remove(target.id)
    if (editingId.value === target.id) editing.value = false
    appStore.showSuccess(t('admin.proxyUpstream.endpointDeleted'))
    emit('changed')
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, UPSTREAM_ERROR_NAMESPACE, t('admin.proxyUpstream.deleteFailed')))
  }
}
</script>
