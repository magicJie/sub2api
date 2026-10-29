<template>
  <div>
    <label :for="id" class="input-label">{{ t('admin.proxyUpstream.chooseGroup') }}</label>
    <Select
      :id="id"
      :model-value="modelValue"
      :options="options"
      :aria-label="t('admin.proxyUpstream.chooseGroup')"
      @update:model-value="emit('update:modelValue', $event as number | null)"
    />
    <p v-if="error" class="mt-1 text-sm text-red-600" role="alert">{{ error }}</p>
    <p class="input-hint mt-1">{{ t('admin.proxyUpstream.failClosed') }}</p>
  </div>
</template>
<script setup lang="ts">
// 前置组（fork）：落地代理的前置组选择
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import { useUpstreamGroups } from './useUpstreamGroups'
defineProps<{ modelValue: number | null; id: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: number | null] }>()
const { t } = useI18n()
const { groupOptions, error } = useUpstreamGroups()
const options = computed(() => [
  { value: null, label: t('admin.proxyUpstream.chooseGroup') },
  ...groupOptions.value
])
</script>
