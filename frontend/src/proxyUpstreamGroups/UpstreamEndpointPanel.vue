<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between gap-3">
      <p class="text-sm text-gray-500">{{ t('admin.proxyUpstream.endpointHint') }}</p>
      <button type="button" class="btn btn-primary btn-sm shrink-0" data-test="aio-create" @click="startCreateAIO">
        <Icon name="plus" size="sm" class="mr-1" />
        {{ t('admin.proxyUpstream.createAio') }}
      </button>
    </div>

    <!-- AIO 编辑 / 新建 -->
    <form
      v-if="editingAIO"
      data-test="aio-form"
      class="space-y-4 rounded-lg border border-gray-200 p-4 dark:border-dark-600"
      @submit.prevent="saveAIO"
    >
      <h3 class="text-base font-semibold">
        {{ aioEditingId ? t('admin.proxyUpstream.editAio') : t('admin.proxyUpstream.createAio') }}
      </h3>
      <div class="grid gap-4 sm:grid-cols-2">
        <label class="block">
          <span class="input-label">{{ t('admin.proxyUpstream.name') }}</span>
          <input v-model="aioForm.name" data-test="aio-name" class="input w-full" required maxlength="255" />
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.proxyUpstream.host') }}</span>
          <input v-model="aioForm.host" data-test="aio-host" class="input w-full" required maxlength="255" />
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.proxyUpstream.protocol') }}</span>
          <select v-model="aioForm.protocol" data-test="aio-protocol" class="input w-full">
            <option v-for="p in protocols" :key="p" :value="p">{{ p.toUpperCase() }}</option>
          </select>
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.proxyUpstream.status') }}</span>
          <select v-model="aioForm.status" class="input w-full">
            <option value="active">{{ t('admin.proxyUpstream.active') }}</option>
            <option value="inactive">{{ t('admin.proxyUpstream.inactive') }}</option>
          </select>
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.proxyUpstream.username') }}</span>
          <input v-model="aioForm.username" class="input w-full" autocomplete="off" />
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.proxyUpstream.password') }}</span>
          <input
            v-model="aioForm.password"
            data-test="aio-password"
            type="password"
            class="input w-full"
            autocomplete="new-password"
            :placeholder="aioEditingHasPassword ? t('admin.proxyUpstream.passwordKeep') : ''"
          />
        </label>
      </div>
      <p class="input-hint">{{ t('admin.proxyUpstream.aioHint') }}</p>
      <p v-if="aioEditingId" class="input-hint">{{ t('admin.proxyUpstream.passwordHint') }}</p>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="editingAIO = false">{{ t('common.cancel') }}</button>
        <button type="submit" class="btn btn-primary" :disabled="saving">{{ t('common.save') }}</button>
      </div>
    </form>

    <!-- 端口编辑 / 新建 -->
    <form
      v-if="editingPort"
      data-test="port-form"
      class="space-y-4 rounded-lg border border-gray-200 p-4 dark:border-dark-600"
      @submit.prevent="savePort"
    >
      <h3 class="text-base font-semibold">
        {{ portEditingId ? t('admin.proxyUpstream.editEndpoint') : t('admin.proxyUpstream.createEndpoint') }}
      </h3>
      <div class="grid gap-4 sm:grid-cols-2">
        <label class="block">
          <span class="input-label">{{ t('admin.proxyUpstream.aio') }}</span>
          <select v-model.number="portForm.aio_id" data-test="port-aio" class="input w-full" required>
            <option v-for="aio in aios" :key="aio.id" :value="aio.id">{{ aio.name }} ({{ aio.host }})</option>
          </select>
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.proxyUpstream.name') }}</span>
          <input
            v-model="portForm.name"
            data-test="endpoint-name"
            class="input w-full"
            required
            maxlength="255"
            :placeholder="t('admin.proxyUpstream.portNamePlaceholder')"
          />
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.proxyUpstream.port') }}</span>
          <input
            v-model.number="portForm.port"
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
          <span class="input-label">{{ t('admin.proxyUpstream.status') }}</span>
          <select v-model="portForm.status" class="input w-full">
            <option value="active">{{ t('admin.proxyUpstream.active') }}</option>
            <option value="inactive">{{ t('admin.proxyUpstream.inactive') }}</option>
          </select>
        </label>
      </div>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="editingPort = false">{{ t('common.cancel') }}</button>
        <button type="submit" class="btn btn-primary" :disabled="saving">{{ t('common.save') }}</button>
      </div>
    </form>

    <p v-if="aios.length === 0" class="py-4 text-sm text-gray-500">{{ t('admin.proxyUpstream.noAios') }}</p>
    <section
      v-for="aio in aios"
      :key="aio.id"
      data-test="aio-card"
      class="overflow-hidden rounded-lg border border-gray-200 dark:border-dark-600"
    >
      <header class="flex flex-wrap items-center justify-between gap-3 bg-gray-50 p-3 dark:bg-dark-700">
        <div class="min-w-0">
          <span class="font-medium text-gray-900 dark:text-white">{{ aio.name }}</span>
          <span class="badge ml-2" :class="aio.status === 'active' ? 'badge-success' : 'badge-gray'">
            {{ aio.status === 'active' ? t('admin.proxyUpstream.active') : t('admin.proxyUpstream.inactive') }}
          </span>
          <div class="mt-0.5 text-xs text-gray-500">
            <code class="code">{{ aio.protocol }}://{{ aio.host }}</code>
            <span v-if="aio.username || aio.has_password">
              · {{ aio.username || '—' }}{{ aio.has_password ? ' · ••••••' : '' }}
            </span>
          </div>
        </div>
        <div class="flex shrink-0 gap-2">
          <button type="button" class="btn btn-secondary btn-sm" data-test="port-create" @click="startCreatePort(aio)">
            <Icon name="plus" size="sm" class="mr-1" />
            {{ t('admin.proxyUpstream.createEndpoint') }}
          </button>
          <button type="button" class="btn btn-secondary btn-sm" data-test="aio-edit" @click="startEditAIO(aio)">
            {{ t('common.edit') }}
          </button>
          <button type="button" class="btn btn-danger btn-sm" data-test="aio-delete" @click="deleting = { kind: 'aio', id: aio.id, name: aio.name }">
            {{ t('common.delete') }}
          </button>
        </div>
      </header>
      <p v-if="portsOf(aio.id).length === 0" class="p-3 text-sm text-gray-500">{{ t('admin.proxyUpstream.noEndpoints') }}</p>
      <table v-else class="w-full text-left text-sm">
        <thead class="text-xs text-gray-500">
          <tr>
            <th class="p-3">{{ t('admin.proxyUpstream.name') }}</th>
            <th class="p-3">{{ t('admin.proxyUpstream.port') }}</th>
            <th class="p-3">{{ t('admin.proxyUpstream.status') }}</th>
            <th class="p-3">{{ t('admin.proxyUpstream.health') }}</th>
            <th class="p-3">{{ t('admin.proxyUpstream.usedBy') }}</th>
            <th class="p-3"></th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="endpoint in portsOf(aio.id)"
            :key="endpoint.id"
            data-test="endpoint-row"
            class="border-t border-gray-100 align-top dark:border-dark-700"
          >
            <td class="p-3 font-medium text-gray-900 dark:text-white">{{ endpoint.name }}</td>
            <td class="p-3"><code class="code text-xs">{{ endpoint.port }}</code></td>
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
              <button type="button" class="btn btn-secondary btn-sm" @click="startEditPort(endpoint)">
                {{ t('common.edit') }}
              </button>
              <button
                type="button"
                class="btn btn-danger btn-sm ml-2"
                @click="deleting = { kind: 'port', id: endpoint.id, name: `${aio.name} / ${endpoint.name}` }"
              >
                {{ t('common.delete') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </section>

    <ConfirmDialog
      :show="deleting !== null"
      :title="deleting?.kind === 'aio' ? t('admin.proxyUpstream.deleteAio') : t('admin.proxyUpstream.deleteEndpoint')"
      :message="
        deleting?.kind === 'aio'
          ? t('admin.proxyUpstream.deleteAioConfirm', { name: deleting?.name ?? '' })
          : t('admin.proxyUpstream.deleteEndpointConfirm', { name: deleting?.name ?? '' })
      "
      danger
      @confirm="remove"
      @cancel="deleting = null"
    />
  </div>
</template>

<script setup lang="ts">
// 前置组（fork）：AIO 实例与其监听端口（前置端点）的增删改与健康状态。
// 主机、协议与认证在 AIO 上维护，端口只填名称与端口号。
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import { extractI18nErrorMessage } from '@/utils/apiError'
import {
  upstreamAIOsAPI,
  upstreamEndpointsAPI,
  type UpstreamAIO,
  type UpstreamAIOInput,
  type UpstreamEndpoint,
  type UpstreamEndpointInput,
  type UpstreamEndpointProtocol
} from './api'
import UpstreamHealthBadge from './UpstreamHealthBadge.vue'
import { UPSTREAM_ERROR_NAMESPACE } from './useUpstreamGroups'

const props = defineProps<{ aios: UpstreamAIO[]; endpoints: UpstreamEndpoint[] }>()
const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const appStore = useAppStore()
const protocols: UpstreamEndpointProtocol[] = ['socks5', 'socks5h', 'http', 'https']

const aioDefaults = (): Required<UpstreamAIOInput> => ({
  name: '',
  protocol: 'socks5',
  host: '',
  username: '',
  password: '',
  status: 'active'
})
const aioForm = reactive(aioDefaults())
const editingAIO = ref(false)
const aioEditingId = ref<number | null>(null)
const aioEditingHasPassword = computed(
  () => props.aios.find((a) => a.id === aioEditingId.value)?.has_password ?? false
)

const portDefaults = (aioId = 0): UpstreamEndpointInput => ({ aio_id: aioId, name: '', port: 17911, status: 'active' })
const portForm = reactive(portDefaults())
const editingPort = ref(false)
const portEditingId = ref<number | null>(null)

const saving = ref(false)
const deleting = ref<{ kind: 'aio' | 'port'; id: number; name: string } | null>(null)

const portsOf = (aioId: number) => props.endpoints.filter((e) => e.aio_id === aioId)

function startCreateAIO() {
  Object.assign(aioForm, aioDefaults())
  aioEditingId.value = null
  editingPort.value = false
  editingAIO.value = true
}
function startEditAIO(aio: UpstreamAIO) {
  Object.assign(aioForm, {
    name: aio.name,
    protocol: aio.protocol,
    host: aio.host,
    username: aio.username,
    password: '',
    status: aio.status
  })
  aioEditingId.value = aio.id
  editingPort.value = false
  editingAIO.value = true
}
async function saveAIO() {
  saving.value = true
  const input: UpstreamAIOInput = {
    name: aioForm.name.trim(),
    protocol: aioForm.protocol,
    host: aioForm.host.trim(),
    username: aioForm.username.trim(),
    status: aioForm.status
  }
  // 更新时留空即保持原密码，不发送 password
  if (aioForm.password) input.password = aioForm.password
  try {
    if (aioEditingId.value) await upstreamAIOsAPI.update(aioEditingId.value, input)
    else await upstreamAIOsAPI.create(input)
    aioForm.password = ''
    editingAIO.value = false
    appStore.showSuccess(t('admin.proxyUpstream.aioSaved'))
    emit('changed')
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, UPSTREAM_ERROR_NAMESPACE, t('admin.proxyUpstream.saveFailed')))
  } finally {
    saving.value = false
  }
}

function startCreatePort(aio: UpstreamAIO) {
  // 默认端口号取该 AIO 已有端口的下一个
  const used = portsOf(aio.id).map((e) => e.port)
  Object.assign(portForm, portDefaults(aio.id), used.length ? { port: Math.min(Math.max(...used) + 1, 65535) } : {})
  portEditingId.value = null
  editingAIO.value = false
  editingPort.value = true
}
function startEditPort(endpoint: UpstreamEndpoint) {
  Object.assign(portForm, { aio_id: endpoint.aio_id, name: endpoint.name, port: endpoint.port, status: endpoint.status })
  portEditingId.value = endpoint.id
  editingAIO.value = false
  editingPort.value = true
}
async function savePort() {
  saving.value = true
  const input: UpstreamEndpointInput = {
    aio_id: portForm.aio_id,
    name: portForm.name.trim(),
    port: portForm.port,
    status: portForm.status
  }
  try {
    if (portEditingId.value) await upstreamEndpointsAPI.update(portEditingId.value, input)
    else await upstreamEndpointsAPI.create(input)
    editingPort.value = false
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
    if (target.kind === 'aio') {
      await upstreamAIOsAPI.remove(target.id)
      if (aioEditingId.value === target.id) editingAIO.value = false
      appStore.showSuccess(t('admin.proxyUpstream.aioDeleted'))
    } else {
      await upstreamEndpointsAPI.remove(target.id)
      if (portEditingId.value === target.id) editingPort.value = false
      appStore.showSuccess(t('admin.proxyUpstream.endpointDeleted'))
    }
    emit('changed')
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, UPSTREAM_ERROR_NAMESPACE, t('admin.proxyUpstream.deleteFailed')))
  }
}
</script>
