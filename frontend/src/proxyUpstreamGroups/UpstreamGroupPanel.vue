<template>
  <div class="grid min-h-[360px] gap-5 md:grid-cols-[220px_minmax(0,1fr)]">
    <aside class="space-y-2">
      <button type="button" class="btn btn-primary w-full" @click="startCreate">
        <Icon name="plus" size="sm" class="mr-1" />
        {{ t('admin.proxyUpstream.createGroup') }}
      </button>
      <p v-if="groups.length === 0" class="py-4 text-sm text-gray-500">{{ t('admin.proxyUpstream.noGroups') }}</p>
      <button
        v-for="group in groups"
        :key="group.id"
        type="button"
        data-test="group-item"
        class="w-full rounded-lg border p-3 text-left text-sm transition-colors"
        :class="
          selectedId === group.id && !creating
            ? 'border-primary-500 bg-primary-50 dark:bg-primary-900/20'
            : 'border-gray-200 hover:bg-gray-50 dark:border-dark-600 dark:hover:bg-dark-700'
        "
        @click="selectGroup(group.id)"
      >
        <span class="block truncate font-medium text-gray-900 dark:text-white">{{ group.name }}</span>
        <span class="mt-1 block text-xs text-gray-500">
          {{ group.status === 'active' ? t('admin.proxyUpstream.active') : t('admin.proxyUpstream.inactive') }} ·
          {{ t('admin.proxyUpstream.slotSummary', { available: availableSlots(group), total: group.slots.length }) }}
        </span>
      </button>
    </aside>

    <section class="min-w-0">
      <!-- 编辑 / 新建 -->
      <form v-if="editing" class="space-y-4" @submit.prevent="save">
        <h3 class="text-base font-semibold">
          {{ creating ? t('admin.proxyUpstream.createGroup') : t('admin.proxyUpstream.editGroup') }}
        </h3>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="block">
            <span class="input-label">{{ t('admin.proxyUpstream.name') }}</span>
            <input v-model="form.name" data-test="group-name" class="input w-full" required maxlength="255" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('admin.proxyUpstream.status') }}</span>
            <select v-model="form.status" class="input w-full">
              <option value="active">{{ t('admin.proxyUpstream.active') }}</option>
              <option value="inactive">{{ t('admin.proxyUpstream.inactive') }}</option>
            </select>
          </label>
          <label class="block sm:col-span-2">
            <span class="input-label">{{ t('admin.proxyUpstream.probeUrl') }}</span>
            <input v-model="form.probe_url" type="url" class="input w-full" required />
          </label>
          <label class="block">
            <span class="input-label">{{ t('admin.proxyUpstream.probeInterval') }}</span>
            <input v-model.number="form.probe_interval_seconds" type="number" min="5" max="3600" step="1" class="input w-full" required />
          </label>
          <label class="block">
            <span class="input-label">{{ t('admin.proxyUpstream.failback') }}</span>
            <input v-model.number="form.failback_seconds" type="number" min="0" max="86400" step="1" class="input w-full" required />
          </label>
          <label class="block">
            <span class="input-label">{{ t('admin.proxyUpstream.failThreshold') }}</span>
            <input v-model.number="form.fail_threshold" type="number" min="1" max="20" step="1" class="input w-full" required />
          </label>
          <label class="block">
            <span class="input-label">{{ t('admin.proxyUpstream.recoverThreshold') }}</span>
            <input v-model.number="form.recover_threshold" type="number" min="1" max="20" step="1" class="input w-full" required />
          </label>
        </div>
        <p class="input-hint">{{ t('admin.proxyUpstream.probeHint') }}</p>

        <fieldset class="space-y-3 rounded-lg border border-gray-200 p-3 dark:border-dark-600">
          <legend class="px-1 text-sm font-medium">{{ t('admin.proxyUpstream.slots') }}</legend>
          <p class="input-hint">{{ t('admin.proxyUpstream.slotHint') }}</p>
          <p v-if="endpoints.length === 0" class="text-sm text-amber-600">{{ t('admin.proxyUpstream.noEndpointsForSlot') }}</p>
          <div
            v-for="(slot, slotIndex) in form.slots"
            :key="slot.key"
            data-test="slot-editor"
            class="space-y-2 rounded-lg bg-gray-50 p-3 dark:bg-dark-700"
          >
            <div class="flex items-center gap-2">
              <span class="w-6 shrink-0 text-xs text-gray-500">#{{ slotIndex + 1 }}</span>
              <input
                v-model="slot.name"
                data-test="slot-name"
                class="input min-w-0 flex-1"
                required
                maxlength="255"
                :aria-label="t('admin.proxyUpstream.slotName')"
                :placeholder="t('admin.proxyUpstream.slotName')"
              />
              <button
                type="button"
                class="btn btn-secondary btn-sm"
                :disabled="slotIndex === 0"
                :aria-label="t('admin.proxyUpstream.moveUp')"
                @click="move(form.slots, slotIndex, -1)"
              >
                <Icon name="arrowUp" size="sm" />
              </button>
              <button
                type="button"
                class="btn btn-secondary btn-sm"
                :disabled="slotIndex === form.slots.length - 1"
                :aria-label="t('admin.proxyUpstream.moveDown')"
                @click="move(form.slots, slotIndex, 1)"
              >
                <Icon name="arrowDown" size="sm" />
              </button>
              <button
                type="button"
                class="btn btn-danger btn-sm"
                data-test="slot-remove"
                :disabled="form.slots.length <= 1"
                :aria-label="t('admin.proxyUpstream.removeSlot')"
                @click="form.slots.splice(slotIndex, 1)"
              >
                <Icon name="trash" size="sm" />
              </button>
            </div>
            <ol class="space-y-1 pl-8">
              <li
                v-for="(endpointId, endpointIndex) in slot.endpoint_ids"
                :key="endpointId"
                data-test="slot-endpoint"
                class="flex items-center gap-2 text-sm"
              >
                <span class="badge shrink-0" :class="endpointIndex === 0 ? 'badge-primary' : 'badge-gray'">
                  {{ endpointIndex === 0 ? t('admin.proxyUpstream.primary') : t('admin.proxyUpstream.backup') }}
                </span>
                <span class="min-w-0 flex-1 truncate">{{ endpointLabel(endpointId) }}</span>
                <button
                  type="button"
                  class="rounded p-1 text-gray-400 hover:text-primary-600 disabled:opacity-30"
                  :disabled="endpointIndex === 0"
                  :aria-label="t('admin.proxyUpstream.moveUp')"
                  @click="move(slot.endpoint_ids, endpointIndex, -1)"
                >
                  <Icon name="arrowUp" size="sm" />
                </button>
                <button
                  type="button"
                  class="rounded p-1 text-gray-400 hover:text-primary-600 disabled:opacity-30"
                  :disabled="endpointIndex === slot.endpoint_ids.length - 1"
                  :aria-label="t('admin.proxyUpstream.moveDown')"
                  @click="move(slot.endpoint_ids, endpointIndex, 1)"
                >
                  <Icon name="arrowDown" size="sm" />
                </button>
                <button
                  type="button"
                  class="rounded p-1 text-gray-400 hover:text-red-600"
                  :aria-label="t('admin.proxyUpstream.removeEndpoint')"
                  @click="slot.endpoint_ids.splice(endpointIndex, 1)"
                >
                  <Icon name="x" size="sm" />
                </button>
              </li>
            </ol>
            <div class="pl-8">
              <select
                class="input w-full text-sm"
                data-test="slot-add-endpoint"
                :aria-label="t('admin.proxyUpstream.addEndpoint')"
                :disabled="addableEndpoints(slot.endpoint_ids).length === 0"
                @change="addEndpoint(slot.endpoint_ids, $event)"
              >
                <option value="">{{ t('admin.proxyUpstream.addEndpoint') }}</option>
                <option v-for="endpoint in addableEndpoints(slot.endpoint_ids)" :key="endpoint.id" :value="endpoint.id">
                  {{ endpointOptionLabel(endpoint) }}
                </option>
              </select>
            </div>
          </div>
          <button type="button" class="btn btn-secondary btn-sm" data-test="slot-add" @click="addSlot">
            <Icon name="plus" size="sm" class="mr-1" />
            {{ t('admin.proxyUpstream.addSlot') }}
          </button>
        </fieldset>

        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="cancelEdit">{{ t('common.cancel') }}</button>
          <button type="submit" class="btn btn-primary" :disabled="saving">{{ t('common.save') }}</button>
        </div>
      </form>

      <!-- 详情 -->
      <div v-else-if="selected" class="space-y-4">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ selected.name }}</h3>
            <p class="mt-1 text-sm text-gray-500">
              {{ selected.status === 'active' ? t('admin.proxyUpstream.active') : t('admin.proxyUpstream.inactive') }} ·
              {{ t('admin.proxyUpstream.proxyCount', { n: selected.proxy_count }) }}
            </p>
          </div>
          <div class="flex gap-2">
            <button type="button" class="btn btn-secondary btn-sm" @click="startEdit">{{ t('common.edit') }}</button>
            <button type="button" class="btn btn-danger btn-sm" @click="confirmDelete = true">{{ t('common.delete') }}</button>
          </div>
        </div>
        <dl class="grid gap-2 rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-700 sm:grid-cols-2">
          <div class="sm:col-span-2">
            <dt class="text-xs text-gray-500">{{ t('admin.proxyUpstream.probeUrl') }}</dt>
            <dd class="break-all">{{ selected.probe_url }}</dd>
          </div>
          <div>
            <dt class="text-xs text-gray-500">{{ t('admin.proxyUpstream.probeInterval') }}</dt>
            <dd>{{ selected.probe_interval_seconds }}</dd>
          </div>
          <div>
            <dt class="text-xs text-gray-500">{{ t('admin.proxyUpstream.failback') }}</dt>
            <dd>{{ selected.failback_seconds }}</dd>
          </div>
          <div>
            <dt class="text-xs text-gray-500">{{ t('admin.proxyUpstream.failThreshold') }}</dt>
            <dd>{{ selected.fail_threshold }}</dd>
          </div>
          <div>
            <dt class="text-xs text-gray-500">{{ t('admin.proxyUpstream.recoverThreshold') }}</dt>
            <dd>{{ selected.recover_threshold }}</dd>
          </div>
        </dl>
        <p class="text-sm text-gray-500">{{ t('admin.proxyUpstream.failClosed') }}</p>
        <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-600">
          <table class="w-full text-left text-sm">
            <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-700">
              <tr>
                <th class="p-3">{{ t('admin.proxyUpstream.slot') }}</th>
                <th class="p-3">{{ t('admin.proxyUpstream.slotState') }}</th>
                <th class="p-3">{{ t('admin.proxyUpstream.currentEndpoint') }}</th>
                <th class="p-3">{{ t('admin.proxyUpstream.endpointsTab') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="slot in sortedSlots(selected)"
                :key="slot.id"
                data-test="slot-row"
                class="border-t border-gray-100 align-top dark:border-dark-700"
              >
                <td class="p-3 font-medium">{{ slot.name }}</td>
                <td class="p-3">
                  <span v-if="!slot.runtime" class="badge badge-gray">{{ t('admin.proxyUpstream.unknown') }}</span>
                  <span v-else class="badge" :class="slot.runtime.available ? 'badge-success' : 'badge-danger'">
                    {{ slot.runtime.available ? t('admin.proxyUpstream.available') : t('admin.proxyUpstream.unavailable') }}
                  </span>
                  <div v-if="slot.runtime?.available && slot.runtime.available_since" class="mt-0.5 text-xs text-gray-500">
                    {{ t('admin.proxyUpstream.availableSince') }}: {{ formatDateTime(slot.runtime.available_since) }}
                  </div>
                </td>
                <td class="p-3" data-test="slot-current">
                  {{ slot.runtime?.current_endpoint_id ? endpointName(slot.runtime.current_endpoint_id) : '—' }}
                </td>
                <td class="p-3">
                  <ol class="space-y-1">
                    <li v-for="(endpointId, index) in slot.endpoint_ids" :key="endpointId" class="flex items-center gap-2">
                      <span class="text-xs text-gray-500">
                        {{ index === 0 ? t('admin.proxyUpstream.primary') : t('admin.proxyUpstream.backup') }}
                      </span>
                      <span class="truncate">{{ endpointName(endpointId) }}</span>
                      <UpstreamHealthBadge :health="endpointById.get(endpointId)?.health" />
                    </li>
                  </ol>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </section>

    <ConfirmDialog
      :show="confirmDelete"
      :title="t('admin.proxyUpstream.deleteGroup')"
      :message="t('admin.proxyUpstream.deleteGroupConfirm', { name: selected?.name ?? '' })"
      danger
      @confirm="remove"
      @cancel="confirmDelete = false"
    />
  </div>
</template>

<script setup lang="ts">
// 前置组（fork）：前置组 = 有序槽位；槽位 = 有序端点列表（第一个为主，其余备用）。
// 落地按 rendezvous hash 在可用槽位间黏滞，槽位顺序只影响展示与编辑。
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import { extractI18nErrorMessage } from '@/utils/apiError'
import {
  upstreamGroupsAPI,
  type UpstreamEndpoint,
  type UpstreamGroup,
  type UpstreamGroupInput,
  type UpstreamStatus
} from './api'
import UpstreamHealthBadge from './UpstreamHealthBadge.vue'
import { UPSTREAM_ERROR_NAMESPACE } from './useUpstreamGroups'

const props = defineProps<{ groups: UpstreamGroup[]; endpoints: UpstreamEndpoint[] }>()
const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const appStore = useAppStore()

interface SlotDraft {
  key: number
  id?: number
  name: string
  endpoint_ids: number[]
}
let slotKey = 0
const newSlot = (index: number): SlotDraft => ({
  key: ++slotKey,
  name: t('admin.proxyUpstream.slotDefaultName', { n: index + 1 }),
  endpoint_ids: []
})
const defaults = () => ({
  name: '',
  status: 'active' as UpstreamStatus,
  probe_url: 'https://www.gstatic.com/generate_204',
  probe_interval_seconds: 30,
  fail_threshold: 3,
  recover_threshold: 2,
  failback_seconds: 300,
  slots: [newSlot(0)] as SlotDraft[]
})
const form = reactive(defaults())
const selectedId = ref<number | null>(null)
const editing = ref(false)
const creating = ref(false)
const saving = ref(false)
const confirmDelete = ref(false)

const selected = computed(() => props.groups.find((g) => g.id === selectedId.value) ?? null)
const endpointById = computed(() => new Map(props.endpoints.map((e) => [e.id, e])))

// 列表刷新后保持选择；选中的组被删除或首次加载时选第一个
watch(
  () => props.groups,
  (groups) => {
    if (editing.value) return
    if (!groups.some((g) => g.id === selectedId.value)) {
      selectedId.value = groups[0]?.id ?? null
      if (!groups.length) startCreate()
    }
  },
  { immediate: true }
)

const availableSlots = (group: UpstreamGroup) => group.slots.filter((s) => s.runtime?.available).length
const sortedSlots = (group: UpstreamGroup) => [...group.slots].sort((a, b) => a.position - b.position)
const endpointName = (id: number) => endpointById.value.get(id)?.name ?? `#${id}`
const endpointOptionLabel = (e: UpstreamEndpoint) =>
  `${e.name} (${e.protocol} ${e.host}:${e.port})${e.status === 'inactive' ? ` · ${t('admin.proxyUpstream.inactive')}` : ''}`
const endpointLabel = (id: number) => {
  const endpoint = endpointById.value.get(id)
  return endpoint ? endpointOptionLabel(endpoint) : `${t('admin.proxyUpstream.missingEndpoint')} #${id}`
}
const addableEndpoints = (ids: number[]) => props.endpoints.filter((e) => !ids.includes(e.id))

function move<T>(list: T[], index: number, delta: number) {
  const target = index + delta
  if (target < 0 || target >= list.length) return
  const [item] = list.splice(index, 1)
  list.splice(target, 0, item)
}
function addEndpoint(ids: number[], event: Event) {
  const select = event.target as HTMLSelectElement
  const id = Number(select.value)
  if (id && !ids.includes(id)) ids.push(id)
  select.value = ''
}
function addSlot() {
  form.slots.push(newSlot(form.slots.length))
}

function selectGroup(id: number) {
  selectedId.value = id
  editing.value = false
  creating.value = false
  confirmDelete.value = false
}
function startCreate() {
  Object.assign(form, defaults())
  creating.value = true
  editing.value = true
}
function startEdit() {
  const group = selected.value
  if (!group) return
  Object.assign(form, {
    name: group.name,
    status: group.status,
    probe_url: group.probe_url,
    probe_interval_seconds: group.probe_interval_seconds,
    fail_threshold: group.fail_threshold,
    recover_threshold: group.recover_threshold,
    failback_seconds: group.failback_seconds,
    slots: sortedSlots(group).map((s) => ({ key: ++slotKey, id: s.id, name: s.name, endpoint_ids: [...s.endpoint_ids] }))
  })
  creating.value = false
  editing.value = true
}
function cancelEdit() {
  editing.value = false
  creating.value = false
  if (!selected.value) selectedId.value = props.groups[0]?.id ?? null
}

async function save() {
  if (form.slots.length === 0) {
    appStore.showError(t('admin.proxyUpstream.slotRequired'))
    return
  }
  if (form.slots.some((s) => s.endpoint_ids.length === 0)) {
    appStore.showError(t('admin.proxyUpstream.slotEndpointRequired'))
    return
  }
  const input: UpstreamGroupInput = {
    name: form.name.trim(),
    status: form.status,
    probe_url: form.probe_url.trim(),
    probe_interval_seconds: form.probe_interval_seconds,
    fail_threshold: form.fail_threshold,
    recover_threshold: form.recover_threshold,
    failback_seconds: form.failback_seconds,
    slots: form.slots.map((s) => ({
      ...(s.id ? { id: s.id } : {}),
      name: s.name.trim(),
      endpoint_ids: [...new Set(s.endpoint_ids)]
    }))
  }
  saving.value = true
  try {
    const saved =
      !creating.value && selected.value
        ? await upstreamGroupsAPI.update(selected.value.id, input)
        : await upstreamGroupsAPI.create(input)
    selectedId.value = saved.id
    editing.value = false
    creating.value = false
    appStore.showSuccess(t('admin.proxyUpstream.groupSaved'))
    emit('changed')
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, UPSTREAM_ERROR_NAMESPACE, t('admin.proxyUpstream.saveFailed')))
  } finally {
    saving.value = false
  }
}
async function remove() {
  const group = selected.value
  confirmDelete.value = false
  if (!group) return
  try {
    await upstreamGroupsAPI.remove(group.id)
    appStore.showSuccess(t('admin.proxyUpstream.groupDeleted'))
    emit('changed')
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, UPSTREAM_ERROR_NAMESPACE, t('admin.proxyUpstream.deleteFailed')))
  }
}
</script>
