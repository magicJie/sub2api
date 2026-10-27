<template>
  <div
    class="text-[10px]"
    :class="toneClass"
    :data-level="status.level"
    data-testid="account-expiry-badge"
  >
    <template v-if="status.level === 'unset'">{{ t('admin.accounts.expiryBadge.unset') }}</template>
    <template v-else>
      <span v-if="!compact" class="mr-1">{{ t('admin.accounts.expiryBadge.expiresOn', { date: dateText }) }}</span>
      <span class="font-medium">{{ remainingText }}</span>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatDateTime } from '@/utils/format'
import { getAccountExpiryStatus } from './accountExpiry'

// 账号到期日与剩余天数（7 天内琥珀色、已到期红色、未设置弱提示）。
// compact：只显示剩余天数（用于已单独显示日期的到期列）。
const props = defineProps<{ expiresAt: number | null | undefined; compact?: boolean }>()
const { t } = useI18n()

const status = computed(() => getAccountExpiryStatus(props.expiresAt))
const dateText = computed(() =>
  props.expiresAt
    ? formatDateTime(new Date(props.expiresAt * 1000), { year: 'numeric', month: '2-digit', day: '2-digit' }, 'sv-SE')
    : ''
)
const remainingText = computed(() => {
  const { level, days } = status.value
  if (level === 'expired') {
    return days ? t('admin.accounts.expiryBadge.expiredDaysAgo', { days: -days }) : t('admin.accounts.expiryBadge.expiredToday')
  }
  return days ? t('admin.accounts.expiryBadge.daysLeft', { days }) : t('admin.accounts.expiryBadge.lessThanOneDay')
})
const toneClass = computed(() => {
  switch (status.value.level) {
    case 'expired':
      return 'text-red-600 dark:text-red-400'
    case 'soon':
      return 'text-amber-600 dark:text-amber-400'
    case 'ok':
      return 'text-gray-500 dark:text-gray-400'
    default:
      return 'text-gray-400 dark:text-dark-500'
  }
})
</script>
