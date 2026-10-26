<template>
  <div>
    <label :for="id" class="input-label">{{
      allowNone ? text.groupDefault : text.choose
    }}</label>
    <Select
      :id="id"
      :model-value="modelValue"
      :options="options"
      :aria-label="allowNone ? text.groupDefault : text.choose"
      @update:model-value="emit('update:modelValue', $event as number | null)"
    />
    <p v-if="error" class="mt-1 text-sm text-red-600" role="alert">
      {{ error }}
    </p>
    <p v-if="allowNone" class="input-hint mt-1">{{ text.defaultHint }}</p>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import Select from '@/components/common/Select.vue'
import { useHopGroups } from './useHopGroups'
const props = defineProps<{
  modelValue: number | null
  id: string
  allowNone?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [value: number | null] }>()
const { groups, error, text } = useHopGroups()
const options = computed(() => [
  {
    value: null,
    label: props.allowNone ? text.value.noDefault : text.value.choose
  },
  ...groups.value.map((group) => ({
    value: group.id,
    label: `${group.name}${group.status === 'inactive' ? ` (${text.value.inactive})` : ''}`,
    disabled: group.status !== 'active'
  }))
])
</script>
