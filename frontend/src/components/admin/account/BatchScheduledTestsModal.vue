<template>
  <BaseDialog
    :show="show"
    :title="t('admin.scheduledTests.batchTitle')"
    width="wide"
    :close-on-escape="!submitting"
    :close-on-click-outside="false"
    :show-close-button="!submitting"
    @close="handleClose"
  >
    <div class="space-y-4">
      <p class="text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.scheduledTests.batchSubtitle') }}
      </p>

      <!-- Target Scope & Shared Model Notice -->
      <div
        data-testid="batch-schedule-target-summary"
        class="rounded-xl border border-primary-200 bg-primary-50/60 p-3.5 text-xs text-primary-900 dark:border-primary-800/60 dark:bg-primary-900/20 dark:text-primary-100"
      >
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="font-medium">
            {{ t('admin.scheduledTests.batchTargetAccounts') }}:
            <span data-testid="batch-schedule-target-count" class="font-semibold">
              {{ t('admin.scheduledTests.batchTargetAccountsValue', { count: displayTargetIds.length }) }}
            </span>
          </div>
          <span
            v-if="targetIdsPreview"
            data-testid="batch-schedule-target-ids"
            class="font-mono text-[11px] text-primary-700 dark:text-primary-300"
          >
            {{ t('admin.scheduledTests.batchTargetIdsPreview', { ids: targetIdsPreview }) }}
          </span>
        </div>
        <p class="mt-1.5 text-primary-800/90 dark:text-primary-200/90">
          {{ t('admin.scheduledTests.batchSameModelNotice') }}
        </p>
      </div>

      <!-- Mixed Platform Warning -->
      <div
        v-if="selectedPlatforms.length > 1"
        data-testid="batch-schedule-mixed-platform-warning"
        class="rounded-xl border border-amber-200 bg-amber-50 p-3 text-xs text-amber-800 dark:border-amber-800/60 dark:bg-amber-900/20 dark:text-amber-300"
      >
        {{ t('admin.scheduledTests.batchMixedPlatformWarning', { platforms: selectedPlatforms.join(', ') }) }}
      </div>

      <!-- Account Cap Warning -->
      <div
        v-if="targetAccountIds.length > MAX_BATCH_ACCOUNTS"
        data-testid="batch-schedule-cap-warning"
        class="rounded-xl border border-red-200 bg-red-50 p-3 text-xs text-red-700 dark:border-red-800/60 dark:bg-red-900/20 dark:text-red-300"
      >
        {{ t('admin.scheduledTests.batchTargetCapWarning', { count: targetAccountIds.length }) }}
      </div>

      <div>
        <div class="mb-1.5 text-xs font-medium text-gray-600 dark:text-gray-400">
          {{ t('admin.scheduledTests.batchModeLabel') }}
        </div>
        <div class="flex flex-wrap gap-1.5">
          <button
            v-for="mode in (['create', 'edit'] as const)"
            :key="mode"
            type="button"
            :data-testid="`batch-schedule-mode-${mode}`"
            :disabled="submitting"
            :aria-pressed="form.mode === mode"
            :class="[
              'rounded-md border px-2 py-0.5 text-[11px] font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50',
              form.mode === mode
                ? 'border-primary-500 bg-primary-50 text-primary-700 dark:border-primary-400 dark:bg-primary-900/30 dark:text-primary-300'
                : 'border-gray-200 bg-gray-50 text-gray-600 hover:border-gray-300 hover:bg-gray-100 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600'
            ]"
            @click="handleModeChange(mode)"
          >
            {{ t(`admin.scheduledTests.${mode === 'edit' ? 'batchModeEdit' : 'batchModeCreate'}`) }}
          </button>
        </div>
        <p data-testid="batch-schedule-mode-notice" class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
          {{ t(`admin.scheduledTests.${form.mode === 'edit' ? 'batchModeEditNotice' : 'batchModeCreateNotice'}`) }}
        </p>
      </div>

      <!-- Configuration Form -->
      <div class="grid grid-cols-1 gap-3.5 sm:grid-cols-2">
        <div>
          <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
            {{ t('admin.scheduledTests.testType') }}
          </label>
          <div data-testid="batch-schedule-test-type">
            <Select
              :model-value="form.test_type"
              :options="testTypeOptions"
              :searchable="false"
              :disabled="submitting"
              @update:model-value="(val) => handleTestTypeChange(String(val || 'fingerprint'))"
            />
          </div>
        </div>

        <div>
          <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
            {{ t('admin.scheduledTests.model') }}
          </label>
          <Input
            data-testid="batch-schedule-model-input"
            :model-value="form.model_id"
            :disabled="submitting"
            :placeholder="t('admin.scheduledTests.batchModelPlaceholder')"
            @update:model-value="handleModelInputChange"
          />
          <div v-if="effectiveModelOptions.length > 0" class="mt-1.5" data-testid="batch-schedule-model-select">
            <Select
              :model-value="form.model_id"
              :options="effectiveModelOptions"
              :placeholder="t('admin.scheduledTests.batchModelQuickPick')"
              :searchable="true"
              :creatable="true"
              :disabled="submitting"
              @update:model-value="(val) => handleModelInputChange(String(val || ''))"
            />
          </div>
        </div>

        <div v-if="includesFingerprint(form.test_type)">
          <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
            {{ t('admin.scheduledTests.expectedModel') }}
          </label>
          <Input
            data-testid="batch-schedule-expected-model-input"
            :model-value="form.expected_model"
            :disabled="submitting"
            :placeholder="t('admin.scheduledTests.expectedModelPlaceholder')"
            :hint="t('admin.scheduledTests.expectedModelHelp')"
            @update:model-value="handleExpectedModelChange"
          />
        </div>

        <div v-if="includesQuality(form.test_type)">
          <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
            {{ t('admin.scheduledTests.qualityThreshold') }}
          </label>
          <Input
            data-testid="batch-schedule-threshold-input"
            :model-value="form.quality_threshold"
            type="number"
            placeholder="80"
            :disabled="submitting"
            :hint="t('admin.scheduledTests.qualityThresholdHelp')"
            @update:model-value="handleThresholdChange"
          />
        </div>

        <div>
          <label class="mb-1 flex items-center gap-1 text-xs font-medium text-gray-600 dark:text-gray-400">
            {{ t('admin.scheduledTests.cronExpression') }}
            <HelpTooltip>
              <template #trigger>
                <span class="inline-flex h-4 w-4 cursor-help items-center justify-center rounded-full border border-gray-400/70 text-[10px] font-semibold text-gray-400 transition-colors hover:border-primary-500 hover:text-primary-600 dark:border-gray-500 dark:text-gray-500 dark:hover:border-primary-400 dark:hover:text-primary-400">
                  ?
                </span>
              </template>
              <div class="space-y-1.5">
                <p class="font-medium">{{ t('admin.scheduledTests.cronTooltipTitle') }}</p>
                <p>{{ t('admin.scheduledTests.cronTooltipMeaning') }}</p>
                <p>{{ t('admin.scheduledTests.cronTooltipExampleEvery30Min') }}</p>
                <p>{{ t('admin.scheduledTests.cronTooltipExampleHourly') }}</p>
                <p>{{ t('admin.scheduledTests.cronTooltipExampleDaily') }}</p>
                <p>{{ t('admin.scheduledTests.cronTooltipExampleWeekly') }}</p>
                <p>{{ t('admin.scheduledTests.cronTooltipRange') }}</p>
              </div>
            </HelpTooltip>
          </label>
          <Input
            data-testid="batch-schedule-cron-input"
            :model-value="form.cron_expression"
            :disabled="submitting"
            placeholder="0 9 * * *"
            :hint="t('admin.scheduledTests.cronHelp')"
            @update:model-value="handleCronChange"
          />
          <div class="mt-1.5 flex flex-wrap items-center gap-1.5">
            <span class="text-[11px] text-gray-400 dark:text-gray-500">
              {{ t('admin.scheduledTests.batchCronPresets') }}:
            </span>
            <button
              v-for="preset in cronPresets"
              :key="preset.value"
              type="button"
              data-testid="batch-schedule-cron-preset"
              :data-preset-value="preset.value"
              :disabled="submitting"
              :class="[
                'rounded-md border px-2 py-0.5 text-[11px] font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50',
                form.cron_expression.trim() === preset.value
                  ? 'border-primary-500 bg-primary-50 text-primary-700 dark:border-primary-400 dark:bg-primary-900/30 dark:text-primary-300'
                  : 'border-gray-200 bg-gray-50 text-gray-600 hover:border-gray-300 hover:bg-gray-100 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600'
              ]"
              @click="applyCronPreset(preset.value)"
            >
              {{ preset.label }}
            </button>
          </div>
        </div>

        <div>
          <label class="mb-1 flex items-center gap-1 text-xs font-medium text-gray-600 dark:text-gray-400">
            {{ t('admin.scheduledTests.maxResults') }}
            <HelpTooltip>
              <template #trigger>
                <span class="inline-flex h-4 w-4 cursor-help items-center justify-center rounded-full border border-gray-400/70 text-[10px] font-semibold text-gray-400 transition-colors hover:border-primary-500 hover:text-primary-600 dark:border-gray-500 dark:text-gray-500 dark:hover:border-primary-400 dark:hover:text-primary-400">
                  ?
                </span>
              </template>
              <div class="space-y-1.5">
                <p class="font-medium">{{ t('admin.scheduledTests.maxResultsTooltipTitle') }}</p>
                <p>{{ t('admin.scheduledTests.maxResultsTooltipMeaning') }}</p>
                <p>{{ t('admin.scheduledTests.maxResultsTooltipBody') }}</p>
                <p>{{ t('admin.scheduledTests.maxResultsTooltipExample') }}</p>
                <p>{{ t('admin.scheduledTests.maxResultsTooltipRange') }}</p>
              </div>
            </HelpTooltip>
          </label>
          <Input
            data-testid="batch-schedule-max-results-input"
            :model-value="form.max_results"
            type="number"
            placeholder="100"
            :disabled="submitting"
            @update:model-value="handleMaxResultsChange"
          />
        </div>

        <div class="flex items-end">
          <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
            <Toggle
              data-testid="batch-schedule-enabled-toggle"
              :model-value="form.enabled"
              :disabled="submitting"
              @update:model-value="handleEnabledChange"
            />
            {{ t('admin.scheduledTests.enabled') }}
          </label>
        </div>

        <div class="flex items-end">
          <div>
            <label
              :class="[
                'flex items-center gap-2 text-sm',
                isConnectivityType(form.test_type)
                  ? 'text-gray-700 dark:text-gray-300'
                  : 'text-gray-400 dark:text-gray-500'
              ]"
            >
              <Toggle
                data-testid="batch-schedule-auto-recover-toggle"
                :model-value="form.auto_recover"
                :disabled="submitting || !isConnectivityType(form.test_type)"
                @update:model-value="handleAutoRecoverChange"
              />
              {{ t('admin.scheduledTests.autoRecover') }}
            </label>
            <p class="mt-0.5 text-xs text-gray-400 dark:text-gray-500">
              {{
                isConnectivityType(form.test_type)
                  ? t('admin.scheduledTests.autoRecoverHelp')
                  : t('admin.scheduledTests.autoRecoverConnectivityOnly')
              }}
            </p>
          </div>
        </div>
      </div>

      <!-- Upstream Cost & Limitation Notice -->
      <div class="rounded-lg border border-amber-200/80 bg-amber-50/70 p-2.5 text-xs text-amber-800 dark:border-amber-800/60 dark:bg-amber-900/20 dark:text-amber-300">
        <div class="font-medium">{{ t('admin.scheduledTests.costNoticeTitle') }}</div>
        <p class="mt-0.5">{{ getCostNoticeText(form.test_type) }}</p>
        <p data-testid="batch-schedule-total-cost" class="mt-1 font-medium">
          {{
            t('admin.scheduledTests.batchTotalCostPerRun', {
              perAccount: perAccountRequestCost,
              accounts: displayTargetIds.length,
              total: totalRequestCostPerRun
            })
          }}
        </p>
        <p v-if="includesFingerprint(form.test_type)" class="mt-1 text-amber-700/90 dark:text-amber-300/90">
          {{ t('admin.scheduledTests.closedSetLimitation') }}
        </p>
      </div>

      <!-- Shared Validation / Request Error -->
      <div
        v-if="requestError"
        data-testid="batch-schedule-error"
        class="rounded-xl border border-red-200 bg-red-50 p-3 text-xs text-red-700 dark:border-red-800/60 dark:bg-red-900/20 dark:text-red-300"
      >
        {{ requestError }}
      </div>

      <!-- Batch Result Summary -->
      <div
        v-if="lastResult"
        data-testid="batch-schedule-result"
        :class="[
          'rounded-xl border p-3.5 text-xs',
          failedResults.length > 0
            ? 'border-amber-200 bg-amber-50/60 dark:border-amber-800/60 dark:bg-amber-900/15'
            : 'border-emerald-200 bg-emerald-50/60 dark:border-emerald-800/60 dark:bg-emerald-900/15'
        ]"
      >
        <div class="flex flex-wrap items-center justify-between gap-2">
          <span class="font-semibold text-gray-800 dark:text-gray-100">
            {{ t('admin.scheduledTests.batchResultTitle') }}
          </span>
          <div class="flex flex-wrap items-center gap-3">
            <span class="inline-flex items-center gap-1 rounded-full bg-emerald-100 px-2.5 py-0.5 font-medium text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-300">
              {{ t('admin.scheduledTests.batchCreatedCount') }}:
              <strong data-testid="batch-schedule-created-count">{{ lastResult.created }}</strong>
            </span>
            <span class="inline-flex items-center gap-1 rounded-full bg-primary-100 px-2.5 py-0.5 font-medium text-primary-700 dark:bg-primary-500/20 dark:text-primary-300">
              {{ t('admin.scheduledTests.batchUpdatedCount') }}:
              <strong data-testid="batch-schedule-updated-count">{{ lastResult.updated }}</strong>
            </span>
            <span class="inline-flex items-center gap-1 rounded-full bg-gray-100 px-2.5 py-0.5 font-medium text-gray-700 dark:bg-dark-700 dark:text-gray-300">
              {{ t('admin.scheduledTests.batchSkippedCount') }}:
              <strong data-testid="batch-schedule-skipped-count">{{ lastResult.skipped }}</strong>
            </span>
            <span
              :class="[
                'inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 font-medium',
                lastResult.failed > 0
                  ? 'bg-red-100 text-red-700 dark:bg-red-500/20 dark:text-red-300'
                  : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-400'
              ]"
            >
              {{ t('admin.scheduledTests.batchFailedCount') }}:
              <strong data-testid="batch-schedule-failed-count">{{ lastResult.failed }}</strong>
            </span>
          </div>
        </div>

        <!-- Failed Accounts List -->
        <div v-if="failedResults.length > 0" class="mt-3 space-y-1.5">
          <div class="font-medium text-amber-800 dark:text-amber-300">
            {{ t('admin.scheduledTests.batchFailedDetailsTitle') }}
          </div>
          <div class="max-h-40 space-y-1 overflow-y-auto">
            <div
              v-for="item in failedResults"
              :key="item.account_id"
              data-testid="batch-schedule-failed-item"
              :data-account-id="item.account_id"
              class="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-red-200/70 bg-white/90 px-2.5 py-1.5 text-red-700 dark:border-red-900/50 dark:bg-dark-800/90 dark:text-red-300"
            >
              <span class="font-mono font-semibold">
                {{ t('admin.scheduledTests.batchAccountItemLabel', { id: item.account_id }) }}
              </span>
              <span class="text-right">{{ item.error || t('admin.scheduledTests.failed') }}</span>
            </div>
          </div>
        </div>
      </div>

    </div>

    <template #footer>
      <div class="flex flex-wrap items-center justify-end gap-2 pt-2">
        <button
          type="button"
          data-testid="batch-schedule-close-btn"
          :disabled="submitting"
          class="rounded-lg bg-gray-100 px-3.5 py-1.5 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-200 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-dark-600 dark:text-gray-300 dark:hover:bg-dark-500"
          @click="handleClose"
        >
          {{ lastResult && failedResults.length === 0 ? t('common.close') : t('common.cancel') }}
        </button>

        <button
          v-if="failedResults.length > 0"
          type="button"
          data-testid="batch-schedule-retry-failed-btn"
          :disabled="!canSubmit"
          class="flex items-center gap-1.5 rounded-lg bg-amber-600 px-3.5 py-1.5 text-sm font-medium text-white transition-colors hover:bg-amber-700 disabled:cursor-not-allowed disabled:opacity-50"
          @click="submitBatch"
        >
          <Icon v-if="submitting" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
          {{
            submitting
              ? t('admin.scheduledTests.batchSubmitting')
              : t('admin.scheduledTests.batchRetryFailed', { count: targetAccountIds.length })
          }}
        </button>

        <button
          v-else-if="!lastResult"
          type="button"
          data-testid="batch-schedule-submit-btn"
          :disabled="!canSubmit"
          class="flex items-center gap-1.5 rounded-lg bg-primary-500 px-3.5 py-1.5 text-sm font-medium text-white transition-colors hover:bg-primary-600 disabled:cursor-not-allowed disabled:opacity-50"
          @click="submitBatch"
        >
          <Icon v-if="submitting" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
          {{
            submitting
              ? t('admin.scheduledTests.batchSubmitting')
              : t('admin.scheduledTests.batchSubmit', { count: targetAccountIds.length })
          }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import Input from '@/components/common/Input.vue'
import Toggle from '@/components/common/Toggle.vue'
import { Icon } from '@/components/icons'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { DEFAULT_FINGERPRINT_CRON, resolveDetectionDefaults } from '@/utils/scheduledTestDefaults'
import type {
  BatchCreateScheduledTestPlansResponse,
  BatchScheduledTestPlanResultItem,
  ClaudeModel,
  ScheduledTestType
} from '@/types'

const MAX_BATCH_ACCOUNTS = 1000

const { t } = useI18n()
const appStore = useAppStore()

const props = withDefaults(
  defineProps<{
    show: boolean
    accountIds: number[]
    modelOptions?: SelectOption[]
    selectedPlatforms?: string[]
  }>(),
  {
    modelOptions: () => [],
    selectedPlatforms: () => []
  }
)

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'created', result: BatchCreateScheduledTestPlansResponse): void
}>()

const submitting = ref(false)
const requestError = ref('')
const lastResult = ref<BatchCreateScheduledTestPlansResponse | null>(null)
const initialAccountIds = ref<number[]>([])
const targetAccountIds = ref<number[]>([])
const fetchedModelOptions = ref<SelectOption[]>([])
let sessionId = 0

const form = reactive({
  mode: 'edit' as 'create' | 'edit',
  test_type: 'fingerprint' as ScheduledTestType,
  model_id: '',
  expected_model: '',
  quality_threshold: '80',
  cron_expression: DEFAULT_FINGERPRINT_CRON,
  max_results: '100',
  enabled: true,
  auto_recover: false
})

const normalizeAccountIds = (ids: number[]): number[] => [...new Set(ids)]

const normalizeTestType = (type?: string | null): ScheduledTestType => {
  if (type === 'connectivity' || type === 'fingerprint' || type === 'quality' || type === 'comprehensive') {
    return type
  }
  return 'fingerprint'
}

const isConnectivityType = (type?: string | null): boolean =>
  normalizeTestType(type) === 'connectivity'

const includesFingerprint = (type?: string | null): boolean => {
  const normalized = normalizeTestType(type)
  return normalized === 'fingerprint' || normalized === 'comprehensive'
}

const includesQuality = (type?: string | null): boolean => {
  const normalized = normalizeTestType(type)
  return normalized === 'quality' || normalized === 'comprehensive'
}

const normalizeQualityThreshold = (raw: string | number | undefined): number => {
  const parsed = Number(raw)
  if (!Number.isFinite(parsed) || parsed <= 0) return 80
  return Math.min(100, Math.max(1, Math.round(parsed)))
}

const normalizeMaxResults = (raw: string | number | undefined): number => {
  const parsed = Number(raw)
  if (!Number.isFinite(parsed) || parsed <= 0) return 100
  return Math.min(1000, Math.max(1, Math.round(parsed)))
}

const testTypeOptions = computed<SelectOption[]>(() => [
  { value: 'quality', label: t('admin.scheduledTests.testTypeQuality') },
  { value: 'comprehensive', label: t('admin.scheduledTests.testTypeComprehensive') },
  { value: 'fingerprint', label: t('admin.scheduledTests.testTypeFingerprint') },
  { value: 'connectivity', label: t('admin.scheduledTests.testTypeConnectivity') }
])

const cronPresets = computed(() => [
  { value: '0 9 * * *', label: t('admin.scheduledTests.cronPresetDaily') },
  { value: '0 */6 * * *', label: t('admin.scheduledTests.cronPresetEvery6Hours') },
  { value: '0 * * * *', label: t('admin.scheduledTests.cronPresetHourly') },
  { value: '*/30 * * * *', label: t('admin.scheduledTests.cronPresetEvery30Min') },
  { value: '0 9 * * 1', label: t('admin.scheduledTests.cronPresetWeekly') }
])

const effectiveModelOptions = computed<SelectOption[]>(() => {
  if (props.modelOptions && props.modelOptions.length > 0) {
    return props.modelOptions
  }
  return fetchedModelOptions.value
})

const failedResults = computed<BatchScheduledTestPlanResultItem[]>(() =>
  lastResult.value?.results.filter((item) => item.status === 'failed') ?? []
)

const displayTargetIds = computed<number[]>(() => {
  if (targetAccountIds.value.length > 0) return targetAccountIds.value
  return initialAccountIds.value
})

const targetIdsPreview = computed<string>(() => {
  const ids = displayTargetIds.value
  if (ids.length === 0) return ''
  const preview = ids.slice(0, 12).map((id) => `#${id}`).join(', ')
  if (ids.length <= 12) return preview
  return `${preview} ... (+${ids.length - 12})`
})

const perAccountRequestCost = computed<number>(() => {
  const normalized = normalizeTestType(form.test_type)
  if (normalized === 'comprehensive') return 4
  if (normalized === 'fingerprint') return 3
  return 1
})

const totalRequestCostPerRun = computed<number>(() => {
  return perAccountRequestCost.value * displayTargetIds.value.length
})

const canSubmit = computed<boolean>(() => {
  return (
    !submitting.value &&
    targetAccountIds.value.length > 0 &&
    targetAccountIds.value.length <= MAX_BATCH_ACCOUNTS &&
    form.model_id.trim().length > 0 &&
    form.cron_expression.trim().length > 0
  )
})

const getCostNoticeText = (type?: string | null): string => {
  const normalized = normalizeTestType(type)
  if (normalized === 'fingerprint') return t('admin.scheduledTests.costNoticeFingerprint')
  if (normalized === 'quality') return t('admin.scheduledTests.costNoticeQuality')
  if (normalized === 'comprehensive') return t('admin.scheduledTests.costNoticeComprehensive')
  return t('admin.scheduledTests.costNoticeConnectivity')
}

const resetForm = () => {
  const defaults = resolveDetectionDefaults(props.selectedPlatforms)
  form.mode = 'edit'
  form.test_type = 'fingerprint'
  form.model_id = defaults.model_id
  form.expected_model = defaults.expected_model
  form.quality_threshold = '80'
  form.cron_expression = DEFAULT_FINGERPRINT_CRON
  form.max_results = '100'
  form.enabled = true
  form.auto_recover = false
}

const restoreCompletedTargetIfNeeded = () => {
  requestError.value = ''
  if (lastResult.value && failedResults.value.length === 0) {
    lastResult.value = null
    targetAccountIds.value = [...initialAccountIds.value]
  }
}

const handleModeChange = (mode: 'create' | 'edit') => {
  if (submitting.value) return
  restoreCompletedTargetIfNeeded()
  form.mode = mode
}

const handleTestTypeChange = (type: string) => {
  restoreCompletedTargetIfNeeded()
  const normalized = normalizeTestType(type)
  form.test_type = normalized
  if (normalized !== 'connectivity') {
    form.auto_recover = false
  }
}

const handleModelInputChange = (value: string) => {
  restoreCompletedTargetIfNeeded()
  form.model_id = value
}

const handleExpectedModelChange = (value: string) => {
  restoreCompletedTargetIfNeeded()
  form.expected_model = value
}

const handleThresholdChange = (value: string) => {
  restoreCompletedTargetIfNeeded()
  form.quality_threshold = value
}

const handleCronChange = (value: string) => {
  restoreCompletedTargetIfNeeded()
  form.cron_expression = value
}

const applyCronPreset = (value: string) => {
  if (submitting.value) return
  restoreCompletedTargetIfNeeded()
  form.cron_expression = value
}

const handleMaxResultsChange = (value: string) => {
  restoreCompletedTargetIfNeeded()
  form.max_results = value
}

const handleEnabledChange = (value: boolean) => {
  restoreCompletedTargetIfNeeded()
  form.enabled = value
}

const handleAutoRecoverChange = (value: boolean) => {
  restoreCompletedTargetIfNeeded()
  form.auto_recover = value
}

const maybeFetchFirstAccountModels = async (firstAccountId: number | undefined, currentSession: number) => {
  if (!firstAccountId || props.modelOptions.length > 0) return
  try {
    const models = await adminAPI.accounts.getAvailableModels(firstAccountId)
    if (currentSession !== sessionId) return
    fetchedModelOptions.value = models.map((m: ClaudeModel) => ({
      value: m.id,
      label: m.display_name || m.id
    }))
  } catch {
    if (currentSession === sessionId) {
      fetchedModelOptions.value = []
    }
  }
}

watch(
  () => props.show,
  (visible) => {
    sessionId++
    const currentSession = sessionId
    submitting.value = false
    requestError.value = ''
    lastResult.value = null
    fetchedModelOptions.value = []
    if (visible) {
      resetForm()
      const snapshot = normalizeAccountIds(props.accountIds)
      initialAccountIds.value = snapshot
      targetAccountIds.value = [...snapshot]
      void maybeFetchFirstAccountModels(snapshot[0], currentSession)
    } else {
      initialAccountIds.value = []
      targetAccountIds.value = []
    }
  },
  { immediate: true }
)

onBeforeUnmount(() => {
  sessionId++
})

const submitBatch = async () => {
  if (!canSubmit.value) return
  const currentSession = sessionId
  const idsToSubmit = [...targetAccountIds.value]
  submitting.value = true
  requestError.value = ''

  try {
    const testType = normalizeTestType(form.test_type)
    const response = await adminAPI.scheduledTests.batchCreate({
      account_ids: idsToSubmit,
      mode: form.mode,
      model_id: form.model_id.trim(),
      test_type: testType,
      expected_model: includesFingerprint(testType) ? form.expected_model.trim() : '',
      quality_threshold: includesQuality(testType)
        ? normalizeQualityThreshold(form.quality_threshold)
        : 80,
      cron_expression: form.cron_expression.trim(),
      enabled: form.enabled,
      max_results: normalizeMaxResults(form.max_results),
      auto_recover: testType === 'connectivity' ? form.auto_recover : false
    })

    if (currentSession !== sessionId) return

    lastResult.value = response
    const nextFailedIds = normalizeAccountIds(
      response.results
        .filter((item) => item.status === 'failed')
        .map((item) => item.account_id)
    )
    targetAccountIds.value = nextFailedIds
    emit('created', response)

    if (response.failed > 0 || nextFailedIds.length > 0) {
      const partialMsg = t('admin.scheduledTests.batchCreatePartial', {
        created: response.created,
        updated: response.updated,
        skipped: response.skipped,
        failed: response.failed
      })
      appStore.showWarning(partialMsg)
    } else {
      appStore.showSuccess(
        t('admin.scheduledTests.batchCreateSuccess', {
          created: response.created,
          updated: response.updated,
          skipped: response.skipped
        })
      )
    }
  } catch (error: unknown) {
    if (currentSession !== sessionId) return
    const message = extractApiErrorMessage(error, t('admin.scheduledTests.batchCreateFailed'))
    requestError.value = message
    appStore.showError(message)
  } finally {
    if (currentSession === sessionId) {
      submitting.value = false
    }
  }
}

const handleClose = () => {
  if (submitting.value) return
  emit('close')
}
</script>
