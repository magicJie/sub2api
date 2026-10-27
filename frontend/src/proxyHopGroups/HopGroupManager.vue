<template>
  <button type="button" class="btn btn-secondary" @click="open">
    {{ text.title }}
  </button>
  <BaseDialog
    :show="show"
    :title="text.title"
    width="extra-wide"
    @close="close"
  >
    <p
      class="mb-4 rounded-lg bg-gray-50 p-3 text-sm text-gray-600 dark:bg-dark-700 dark:text-dark-200"
    >
      {{ text.failClosed }}
    </p>
    <div
      v-if="error"
      role="alert"
      class="mb-4 rounded border border-red-300 p-3 text-sm text-red-600"
    >
      {{ error }}
    </div>
    <div class="grid min-h-[360px] gap-5 md:grid-cols-[220px_minmax(0,1fr)]">
      <aside class="space-y-2">
        <button type="button" class="btn btn-primary w-full" @click="newGroup">
          {{ text.create }}
        </button>
        <p v-if="loading" class="py-4 text-sm text-gray-500">
          {{ text.loading }}
        </p>
        <p v-else-if="groups.length === 0" class="py-4 text-sm text-gray-500">
          {{ text.empty }}
        </p>
        <button
          v-for="group in groups"
          :key="group.id"
          type="button"
          class="w-full rounded-lg border p-3 text-left text-sm transition-colors"
          :class="
            selected?.id === group.id
              ? 'border-primary-500 bg-primary-50 dark:bg-primary-900/20'
              : 'border-gray-200 hover:bg-gray-50 dark:border-dark-600 dark:hover:bg-dark-700'
          "
          @click="select(group.id)"
        >
          <span
            class="block truncate font-medium text-gray-900 dark:text-white"
            >{{ group.name }}</span
          >
          <span class="mt-1 block text-xs text-gray-500"
            >{{ group.strategy }} ·
            {{ group.status === 'active' ? text.active : text.inactive }} ·
            {{ group.subscription_count }} {{ text.providers }}</span
          >
        </button>
      </aside>
      <section class="min-w-0">
        <form v-if="editing" class="space-y-4" @submit.prevent="save">
          <h3 class="text-base font-semibold">
            {{ selected ? text.edit : text.create }}
          </h3>
          <div class="grid gap-4 sm:grid-cols-2">
            <label class="block"
              ><span class="input-label">{{ text.name }}</span
              ><input
                v-model="form.name"
                class="input w-full"
                required
                maxlength="255"
            /></label>
            <label class="block"
              ><span class="input-label">{{ text.strategy }}</span
              ><select v-model="form.strategy" class="input w-full">
                <option value="fallback">{{ text.fallback }}</option>
                <option value="url-test">{{ text.urlTest }}</option>
                <option value="load-balance">{{ text.loadBalance }}</option>
              </select></label
            >
            <p
              v-if="form.strategy === 'load-balance'"
              class="input-hint sm:col-span-2"
            >
              {{ text.loadBalanceHint }}
            </p>
            <label class="block sm:col-span-2"
              ><span class="input-label">{{ text.testURL }}</span
              ><input
                v-model="form.test_url"
                type="url"
                class="input w-full"
                required
            /></label>
            <label class="block"
              ><span class="input-label">{{ text.interval }}</span
              ><input
                v-model.number="form.interval_seconds"
                type="number"
                min="1"
                step="1"
                class="input w-full"
                required
            /></label>
            <label class="block"
              ><span class="input-label">{{ text.updateInterval }}</span
              ><input
                v-model.number="form.subscription_update_interval"
                type="number"
                min="1"
                step="1"
                class="input w-full"
                required
            /></label>
            <label v-if="form.strategy === 'url-test'" class="block"
              ><span class="input-label">{{ text.tolerance }}</span
              ><input
                v-model.number="form.tolerance_ms"
                type="number"
                min="0"
                step="1"
                class="input w-full"
                required
            /></label>
            <label class="block"
              ><span class="input-label">{{ text.status }}</span
              ><select v-model="form.status" class="input w-full">
                <option value="active">{{ text.active }}</option>
                <option value="inactive">{{ text.inactive }}</option>
              </select></label
            >
          </div>
          <div class="space-y-2">
            <p v-if="selected" class="text-sm text-gray-500">
              {{ text.subscriptionSaved }}: {{ selected.subscription_count }}
            </p>
            <label v-if="selected" class="flex items-center gap-2 text-sm"
              ><input v-model="replaceSubscriptions" type="checkbox" />{{
                text.replaceSubscriptions
              }}</label
            >
            <label v-if="!selected || replaceSubscriptions" class="block"
              ><span class="input-label">{{ text.subscriptions }}</span
              ><textarea
                v-model="subscriptionText"
                class="input min-h-24 w-full font-mono text-sm"
                autocomplete="off"
                spellcheck="false"
              />
            </label>
            <p class="input-hint">
              {{ selected ? text.keepSubscriptions : text.subscriptionHint }}
            </p>
          </div>
          <fieldset
            class="rounded-lg border border-gray-200 p-3 dark:border-dark-600"
          >
            <legend class="px-1 text-sm font-medium">{{ text.manual }}</legend>
            <p v-if="directProxies.length === 0" class="text-sm text-gray-500">
              {{ text.noManual }}
            </p>
            <div class="max-h-44 space-y-2 overflow-y-auto">
              <label
                v-for="proxy in directProxies"
                :key="proxy.id"
                class="flex items-center gap-2 text-sm"
                ><input
                  v-model="form.member_proxy_ids"
                  type="checkbox"
                  :value="proxy.id"
                /><span>{{ proxy.name }}</span
                ><span class="text-xs text-gray-500"
                  >{{ proxy.protocol }} · #{{ proxy.id }}</span
                ><span
                  v-if="form.member_proxy_ids.includes(proxy.id)"
                  class="ml-auto text-xs text-gray-500"
                  >{{ form.member_proxy_ids.indexOf(proxy.id) + 1 }}</span
                ></label
              >
              <label
                v-for="id in missingMemberIds"
                :key="`missing-${id}`"
                class="flex items-center gap-2 text-sm text-amber-600"
                ><input
                  v-model="form.member_proxy_ids"
                  type="checkbox"
                  :value="id"
                />{{ text.unavailableMember }} #{{ id }}</label
              >
            </div>
            <p class="input-hint mt-2">{{ text.memberOrder }}</p>
          </fieldset>
          <div class="flex justify-end gap-3">
            <button
              type="button"
              class="btn btn-secondary"
              @click="cancelEdit"
            >
              {{ text.cancel }}</button
            ><button type="submit" class="btn btn-primary" :disabled="saving">
              {{ text.save }}
            </button>
          </div>
        </form>
        <div v-else-if="selected" class="space-y-4">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h3 class="text-base font-semibold text-gray-900 dark:text-white">
                {{ selected.name }}
              </h3>
              <p class="mt-1 text-sm text-gray-500">
                {{ selected.strategy }} ·
                {{ selected.status === 'active' ? text.active : text.inactive }}
              </p>
            </div>
            <div class="flex gap-2">
              <button
                type="button"
                class="btn btn-secondary btn-sm"
                @click="editSelected"
              >
                {{ text.edit }}</button
              ><button
                type="button"
                class="btn btn-danger btn-sm"
                @click="confirmDelete = true"
              >
                {{ text.remove }}
              </button>
            </div>
          </div>
          <div
            v-if="confirmDelete"
            class="rounded-lg border border-red-300 p-3"
          >
            <p class="text-sm">{{ text.confirmDelete }}</p>
            <div class="mt-3 flex justify-end gap-2">
              <button
                class="btn btn-secondary btn-sm"
                @click="confirmDelete = false"
              >
                {{ text.cancel }}</button
              ><button
                class="btn btn-danger btn-sm"
                :disabled="saving"
                @click="remove"
              >
                {{ text.remove }}
              </button>
            </div>
          </div>
          <div
            class="flex flex-wrap items-center justify-between gap-2 rounded-lg bg-gray-50 p-3 dark:bg-dark-700"
          >
            <div>
              <p class="text-xs text-gray-500">{{ text.selected }}</p>
              <p class="mt-1 break-all font-medium">
                {{
                  selected.strategy === 'load-balance'
                    ? text.balancedSelection
                    : selected.runtime?.selected || text.noneSelected
                }}
              </p>
            </div>
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              :disabled="refreshing"
              @click="refreshStatus"
            >
              {{ text.refresh }}
            </button>
          </div>
          <p v-if="selected.runtime_error" class="text-sm text-amber-600">
            {{ selected.runtime_error }}
          </p>
          <div
            class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-600"
          >
            <table class="w-full text-left text-sm">
              <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-700">
                <tr>
                  <th class="p-3">{{ text.nodes }}</th>
                  <th class="p-3">{{ text.status }}</th>
                  <th class="p-3">{{ text.delay }}</th>
                  <th class="p-3">{{ text.activeConnections }}</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="node in selected.runtime?.nodes || []"
                  :key="node.name"
                  class="border-t border-gray-100 dark:border-dark-700"
                >
                  <td class="break-all p-3">{{ node.name }}</td>
                  <td class="p-3">
                    <span
                      class="badge"
                      :class="node.alive ? 'badge-success' : 'badge-danger'"
                      >{{ node.alive ? text.alive : text.down }}</span
                    >
                  </td>
                  <td class="p-3">
                    {{ node.alive ? `${node.delay_ms} ms` : '—' }}
                  </td>
                  <td class="p-3">{{ node.active_connections ?? 0 }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <div
            v-for="provider in selected.runtime?.providers || []"
            :key="provider.name"
            class="rounded-lg border border-gray-200 p-3 text-sm dark:border-dark-600"
          >
            <div class="flex justify-between gap-3">
              <span class="break-all font-medium">{{ provider.name }}</span
              ><span class="shrink-0"
                >{{ text.count }}: {{ provider.node_count }}</span
              >
            </div>
            <p class="mt-1 text-xs text-gray-500">
              {{ text.updated }}: {{ date(provider.updated_at) }}
            </p>
            <p v-if="provider.last_error" class="mt-1 text-xs text-amber-600">
              {{ provider.last_error }}
            </p>
          </div>
        </div>
      </section>
    </div>
    <template #footer
      ><button type="button" class="btn btn-secondary" @click="close">
        {{ text.close }}
      </button></template
    >
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, reactive, ref } from 'vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { proxiesAPI } from '@/api/admin/proxies'
import type { Proxy } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { hopGroupsAPI, type HopGroup, type HopGroupInput } from './api'
import { useHopGroupText } from './useHopGroups'
const emit = defineEmits<{ changed: [] }>()
const text = useHopGroupText()
const app = useAppStore()
const show = ref(false)
const groups = ref<HopGroup[]>([])
const proxies = ref<Proxy[]>([])
const selected = ref<HopGroup | null>(null)
const loading = ref(false)
const saving = ref(false)
const refreshing = ref(false)
const editing = ref(false)
const confirmDelete = ref(false)
const error = ref('')
const replaceSubscriptions = ref(false)
const subscriptionText = ref('')
const defaults = (): HopGroupInput => ({
  name: '',
  strategy: 'fallback',
  test_url: 'https://www.gstatic.com/generate_204',
  interval_seconds: 60,
  tolerance_ms: 50,
  member_proxy_ids: [],
  subscription_update_interval: 3600,
  status: 'active'
})
const form = reactive<HopGroupInput>(defaults())
const directProxies = computed(() =>
  proxies.value.filter(
    (proxy) =>
      (!proxy.upstream_mode || proxy.upstream_mode === 'direct') &&
      (proxy.status === 'active' || form.member_proxy_ids.includes(proxy.id))
  )
)
const missingMemberIds = computed(() =>
  form.member_proxy_ids.filter(
    (id) => !directProxies.value.some((proxy) => proxy.id === id)
  )
)
let timer: ReturnType<typeof setInterval> | undefined
let selectionRequest = 0
async function open() {
  show.value = true
  loading.value = true
  error.value = ''
  selected.value = null
  editing.value = false
  try {
    const [loadedGroups, loadedProxies] = await Promise.all([
      hopGroupsAPI.list(),
      proxiesAPI.getAll()
    ])
    groups.value = loadedGroups
    proxies.value = loadedProxies
    if (groups.value[0]) await select(groups.value[0].id)
    else newGroup()
  } catch {
    error.value = text.value.loadFailed
  } finally {
    loading.value = false
  }
  if (!show.value) return
  if (timer) clearInterval(timer)
  timer = setInterval(() => {
    if (!editing.value && selected.value && !refreshing.value)
      void refreshStatus()
  }, 10000)
}
function close() {
  show.value = false
  subscriptionText.value = ''
  selectionRequest++
  if (timer) clearInterval(timer)
  timer = undefined
}
onUnmounted(close)
function cancelEdit() {
  editing.value = false
  subscriptionText.value = ''
}
async function select(id: number) {
  const request = ++selectionRequest
  editing.value = false
  confirmDelete.value = false
  subscriptionText.value = ''
  error.value = ''
  try {
    const group = await hopGroupsAPI.get(id)
    if (request === selectionRequest && show.value) selected.value = group
  } catch {
    error.value = text.value.loadFailed
  }
}
function newGroup() {
  selectionRequest++
  selected.value = null
  Object.assign(form, defaults())
  replaceSubscriptions.value = false
  subscriptionText.value = ''
  editing.value = true
  confirmDelete.value = false
  error.value = ''
}
function editSelected() {
  if (!selected.value) return
  const {
    name,
    strategy,
    test_url,
    interval_seconds,
    tolerance_ms,
    member_proxy_ids,
    subscription_update_interval,
    status
  } = selected.value
  Object.assign(form, {
    name,
    strategy,
    test_url,
    interval_seconds,
    tolerance_ms,
    member_proxy_ids: [...member_proxy_ids],
    subscription_update_interval,
    status
  })
  replaceSubscriptions.value = false
  subscriptionText.value = ''
  editing.value = true
}
async function save() {
  saving.value = true
  error.value = ''
  const input: HopGroupInput = {
    ...form,
    member_proxy_ids: [...form.member_proxy_ids]
  }
  if (!selected.value || replaceSubscriptions.value)
    input.subscription_urls = subscriptionText.value
      .split(/\r?\n/)
      .map((value) => value.trim())
      .filter(Boolean)
  try {
    const saved = selected.value
      ? await hopGroupsAPI.update(selected.value.id, input)
      : await hopGroupsAPI.create(input)
    subscriptionText.value = ''
    groups.value = await hopGroupsAPI.list()
    await select(saved.id)
    app.showSuccess(text.value.saved)
    emit('changed')
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, text.value.saveFailed)
  } finally {
    saving.value = false
  }
}
async function remove() {
  if (!selected.value) return
  saving.value = true
  error.value = ''
  try {
    await hopGroupsAPI.remove(selected.value.id)
    groups.value = await hopGroupsAPI.list()
    selected.value = null
    confirmDelete.value = false
    if (groups.value[0]) await select(groups.value[0].id)
    app.showSuccess(text.value.deleted)
    emit('changed')
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, text.value.deleteFailed)
  } finally {
    saving.value = false
  }
}
async function refreshStatus() {
  if (!selected.value || refreshing.value) return
  const id = selected.value.id
  const request = selectionRequest
  refreshing.value = true
  try {
    const group = await hopGroupsAPI.get(id)
    if (
      request === selectionRequest &&
      show.value &&
      !editing.value &&
      selected.value?.id === id
    )
      selected.value = group
  } catch {
    error.value = text.value.loadFailed
  } finally {
    refreshing.value = false
  }
}
function date(value: string) {
  return value && !value.startsWith('0001-')
    ? new Date(value).toLocaleString()
    : text.value.never
}
</script>
