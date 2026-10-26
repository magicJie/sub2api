<template>
  <BaseDialog :show="show" :title="t('admin.proxyPools.title')" width="extra-wide" @close="emit('close')">
    <ProxyPoolManagerPanel :active="show" @changed="emit('changed')" />
  </BaseDialog>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ProxyPoolManagerPanel from './ProxyPoolManagerPanel.vue'

defineProps<{ show: boolean }>()
const emit = defineEmits<{ close: []; changed: [] }>()
const { t } = useI18n()
</script>
