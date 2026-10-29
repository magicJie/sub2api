<template>
  <span class="badge" :class="badgeClass" :title="health?.last_error || undefined">{{ label }}</span>
</template>
<script setup lang="ts">
// 前置组（fork）：端点健康状态徽标
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UpstreamEndpointHealth } from './api'
const props = defineProps<{ health?: UpstreamEndpointHealth | null }>()
const { t } = useI18n()
const state = computed(() => props.health?.state ?? 'unknown')
const badgeClass = computed(() =>
  state.value === 'healthy' ? 'badge-success' : state.value === 'unhealthy' ? 'badge-danger' : 'badge-gray'
)
const label = computed(() =>
  state.value === 'healthy'
    ? t('admin.proxyUpstream.healthy')
    : state.value === 'unhealthy'
      ? t('admin.proxyUpstream.unhealthy')
      : t('admin.proxyUpstream.unknown')
)
</script>
