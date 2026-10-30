<template>
  <BaseDialog :show="show" :title="t('admin.accounts.enabledPlatforms.title')" width="normal" @close="$emit('close')">
    <div class="space-y-3">
      <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.accounts.enabledPlatforms.hint') }}</p>
      <div class="flex items-center gap-3 text-sm">
        <button type="button" class="text-primary-600 hover:underline dark:text-primary-400" data-test="select-all" @click="selected = allValues()">
          {{ t('admin.accounts.enabledPlatforms.selectAll') }}
        </button>
        <button type="button" class="text-primary-600 hover:underline dark:text-primary-400" data-test="clear" @click="selected = []">
          {{ t('admin.accounts.enabledPlatforms.clear') }}
        </button>
      </div>
      <div class="grid grid-cols-2 gap-2">
        <label
          v-for="option in CONCRETE_PLATFORM_OPTIONS"
          :key="option.value"
          class="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm text-gray-700 hover:bg-gray-50 dark:text-gray-200 dark:hover:bg-dark-700"
        >
          <input
            v-model="selected"
            type="checkbox"
            :value="option.value"
            :data-test="`platform-${option.value}`"
            class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
          />
          <span>{{ option.label }}</span>
        </label>
      </div>
      <p v-if="selected.length === 0" class="text-xs text-amber-600 dark:text-amber-400">
        {{ t('admin.accounts.enabledPlatforms.atLeastOne') }}
      </p>
    </div>
    <template #footer>
      <button type="button" class="btn btn-secondary" @click="$emit('close')">{{ t('common.cancel') }}</button>
      <button type="button" class="btn btn-primary" data-test="save" :disabled="selected.length === 0" @click="save">
        {{ t('common.save') }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'
import { accountEnabledPlatforms, saveAccountEnabledPlatforms } from './accountEnabledPlatforms'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t } = useI18n()

const allValues = () => CONCRETE_PLATFORM_OPTIONS.map(option => option.value as string)
const selected = ref<string[]>([])

watch(
  () => props.show,
  (show) => {
    if (!show) return
    const enabled = accountEnabledPlatforms.value
    selected.value = enabled ? allValues().filter(value => enabled.includes(value)) : allValues()
  },
  { immediate: true }
)

const save = () => {
  if (selected.value.length === 0) return
  // 全选等同于未配置，之后新增的平台也默认启用。
  saveAccountEnabledPlatforms(selected.value.length === allValues().length ? null : selected.value)
  emit('saved')
  emit('close')
}
</script>
