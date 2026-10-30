<template>
  <button type="button" class="btn btn-secondary" @click="open">
    <Icon name="link" size="md" class="mr-2" />
    {{ t('admin.proxyUpstream.title') }}
  </button>
  <BaseDialog :show="show" :title="t('admin.proxyUpstream.title')" width="extra-wide" @close="close">
    <p class="mb-4 rounded-lg bg-gray-50 p-3 text-sm text-gray-600 dark:bg-dark-700 dark:text-dark-200">
      {{ t('admin.proxyUpstream.intro') }}
    </p>
    <div class="mb-4 flex gap-2" role="tablist">
      <button
        v-for="item in tabs"
        :key="item.value"
        type="button"
        role="tab"
        :aria-selected="tab === item.value"
        class="btn btn-sm"
        :class="tab === item.value ? 'btn-primary' : 'btn-secondary'"
        @click="tab = item.value"
      >
        {{ item.label }}
      </button>
    </div>
    <div v-if="error" role="alert" class="mb-4 rounded border border-red-300 p-3 text-sm text-red-600">
      {{ error }}
    </div>
    <p v-if="loading" class="py-4 text-sm text-gray-500">{{ t('common.loading') }}</p>
    <UpstreamGroupPanel
      v-else-if="tab === 'groups'"
      :groups="groups"
      :endpoints="endpoints"
      @changed="onChanged"
    />
    <UpstreamEndpointPanel v-else :aios="aios" :endpoints="endpoints" @changed="onChanged" />
    <template #footer>
      <button type="button" class="btn btn-secondary" @click="close">{{ t('common.close') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
// 前置组（fork）：前置组与 AIO（含监听端口）管理入口（取代首跳组）。列表每 10 秒刷新以展示健康与槽位 runtime。
import { computed, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  upstreamAIOsAPI,
  upstreamEndpointsAPI,
  upstreamGroupsAPI,
  type UpstreamAIO,
  type UpstreamEndpoint,
  type UpstreamGroup
} from './api'
import UpstreamGroupPanel from './UpstreamGroupPanel.vue'
import UpstreamEndpointPanel from './UpstreamEndpointPanel.vue'

const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const show = ref(false)
const tab = ref<'groups' | 'endpoints'>('groups')
const groups = ref<UpstreamGroup[]>([])
const aios = ref<UpstreamAIO[]>([])
const endpoints = ref<UpstreamEndpoint[]>([])
const loading = ref(false)
const refreshing = ref(false)
const error = ref('')
const tabs = computed(() => [
  { value: 'groups' as const, label: t('admin.proxyUpstream.groupsTab') },
  { value: 'endpoints' as const, label: t('admin.proxyUpstream.endpointsTab') }
])
let timer: ReturnType<typeof setInterval> | undefined

async function reload() {
  try {
    const [loadedGroups, loadedAIOs, loadedEndpoints] = await Promise.all([
      upstreamGroupsAPI.list(),
      upstreamAIOsAPI.list(),
      upstreamEndpointsAPI.list()
    ])
    if (!show.value) return
    groups.value = loadedGroups
    aios.value = loadedAIOs
    endpoints.value = loadedEndpoints
    error.value = ''
  } catch {
    error.value = t('admin.proxyUpstream.loadFailed')
  }
}
async function open() {
  show.value = true
  loading.value = true
  await reload()
  loading.value = false
  if (!show.value) return
  if (timer) clearInterval(timer)
  timer = setInterval(async () => {
    if (refreshing.value) return
    refreshing.value = true
    try {
      await reload()
    } finally {
      refreshing.value = false
    }
  }, 10000)
}
function close() {
  show.value = false
  if (timer) clearInterval(timer)
  timer = undefined
}
onUnmounted(close)
async function onChanged() {
  await reload()
  emit('changed')
}
</script>
