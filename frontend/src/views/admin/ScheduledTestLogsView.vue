<template>
  <AppLayout>
    <TablePageLayout>
      <!-- Filters -->
      <template #filters>
        <div class="card p-4 sm:p-6">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <!-- Left: filter fields -->
            <div class="flex flex-1 flex-wrap items-end gap-4">
              <div class="w-full sm:w-auto sm:min-w-[150px]">
                <label class="input-label">{{ t('admin.scheduledTests.logs.platform') }}</label>
                <Select
                  v-model="filters.platform"
                  :options="platformOptions"
                  :searchable="false"
                  :aria-label="t('admin.scheduledTests.logs.platform')"
                  data-testid="detection-logs-filter-platform"
                  @change="handleFilterChange"
                />
              </div>

              <div class="w-full sm:w-auto sm:min-w-[150px]">
                <label class="input-label">{{ t('admin.scheduledTests.logs.status') }}</label>
                <Select
                  v-model="filters.status"
                  :options="statusOptions"
                  :searchable="false"
                  :aria-label="t('admin.scheduledTests.logs.status')"
                  data-testid="detection-logs-filter-status"
                  @change="handleFilterChange"
                />
              </div>

              <div class="w-full sm:w-auto sm:min-w-[180px]">
                <label class="input-label">{{ t('admin.scheduledTests.logs.testType') }}</label>
                <Select
                  v-model="filters.test_type"
                  :options="testTypeOptions"
                  :searchable="false"
                  :aria-label="t('admin.scheduledTests.logs.testType')"
                  data-testid="detection-logs-filter-test-type"
                  @change="handleFilterChange"
                />
              </div>

              <div class="w-full sm:w-auto sm:min-w-[160px]">
                <label class="input-label">{{ t('admin.scheduledTests.logs.fingerprintStatus') }}</label>
                <Select
                  v-model="filters.fingerprint_status"
                  :options="fingerprintStatusOptions"
                  :searchable="false"
                  :aria-label="t('admin.scheduledTests.logs.fingerprintStatus')"
                  data-testid="detection-logs-filter-fingerprint-status"
                  @change="handleFilterChange"
                />
              </div>

              <div class="w-full sm:w-auto sm:w-36">
                <label class="input-label" for="detection-logs-account-id">
                  {{ t('admin.scheduledTests.logs.accountId') }}
                </label>
                <input
                  id="detection-logs-account-id"
                  v-model.trim="filters.account_id"
                  type="number"
                  min="1"
                  step="1"
                  inputmode="numeric"
                  class="input"
                  data-testid="detection-logs-filter-account-id"
                  @change="handleFilterChange"
                  @keyup.enter="handleFilterChange"
                />
              </div>

              <div class="w-full sm:w-auto sm:min-w-[200px]">
                <label class="input-label" for="detection-logs-start-time">
                  {{ t('admin.scheduledTests.logs.startTime') }}
                </label>
                <input
                  id="detection-logs-start-time"
                  v-model="filters.start_time"
                  type="datetime-local"
                  class="input"
                  data-testid="detection-logs-filter-start-time"
                  @change="handleFilterChange"
                />
              </div>

              <div class="w-full sm:w-auto sm:min-w-[200px]">
                <label class="input-label" for="detection-logs-end-time">
                  {{ t('admin.scheduledTests.logs.endTime') }}
                </label>
                <input
                  id="detection-logs-end-time"
                  v-model="filters.end_time"
                  type="datetime-local"
                  class="input"
                  data-testid="detection-logs-filter-end-time"
                  @change="handleFilterChange"
                />
              </div>
            </div>

            <!-- Upper-right: actions -->
            <div class="flex w-full flex-shrink-0 flex-wrap items-center justify-end gap-2 sm:w-auto">
              <button
                type="button"
                class="btn btn-secondary"
                :disabled="loading"
                data-testid="detection-logs-reset-btn"
                @click="resetFilters"
              >
                {{ t('common.reset') }}
              </button>
              <button
                type="button"
                class="btn btn-secondary inline-flex items-center gap-1.5"
                :disabled="loading"
                :title="t('common.refresh')"
                data-testid="detection-logs-refresh-btn"
                @click="fetchLogs()"
              >
                <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
                <span>{{ t('common.refresh') }}</span>
              </button>
            </div>
          </div>
        </div>
      </template>

      <!-- Table -->
      <template #table>
        <div class="table-wrapper">
          <table class="w-full min-w-max divide-y divide-gray-200 dark:divide-dark-700">
            <thead class="table-header bg-gray-50 dark:bg-dark-800">
              <tr>
                <th
                  v-for="column in columns"
                  :key="column.key"
                  scope="col"
                  class="sticky-header-cell px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-dark-400"
                >
                  {{ column.label }}
                </th>
              </tr>
            </thead>

            <tbody class="table-body divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900">
              <!-- Loading skeleton per DataTable -->
              <template v-if="loading">
                <tr v-for="i in 5" :key="`skeleton-${i}`">
                  <td
                    v-for="column in columns"
                    :key="column.key"
                    class="whitespace-nowrap px-4 py-4"
                  >
                    <div class="animate-pulse">
                      <div class="h-4 w-3/4 rounded bg-gray-200 dark:bg-dark-700"></div>
                    </div>
                  </td>
                </tr>
              </template>

              <!-- Empty state per DataTable / AuditLogView -->
              <tr v-else-if="logs.length === 0">
                <td :colspan="columns.length" class="px-4 py-12 text-center text-gray-500 dark:text-dark-400">
                  <div class="flex flex-col items-center py-8" data-testid="detection-logs-empty">
                    <Icon name="shield" size="xl" class="mb-4 h-12 w-12 text-gray-300 dark:text-dark-600" />
                    <p class="text-sm font-medium text-gray-500 dark:text-gray-400">
                      {{ t('admin.scheduledTests.logs.empty') }}
                    </p>
                  </div>
                </td>
              </tr>

              <!-- Data rows + expandable diagnostics detail rows -->
              <template v-else>
                <template v-for="row in logs" :key="row.id">
                  <tr
                    :data-testid="`detection-log-row-${row.id}`"
                    class="cursor-pointer transition-colors hover:bg-gray-50 dark:hover:bg-dark-800"
                    :class="expandedIds.has(row.id) ? 'bg-gray-50/80 dark:bg-dark-800/60' : ''"
                    @click="toggleExpand(row.id)"
                  >
                    <!-- Time + keyboard-accessible expand control -->
                    <td class="whitespace-nowrap px-4 py-4 text-sm text-gray-600 dark:text-gray-300">
                      <div class="flex items-center gap-2">
                        <button
                          type="button"
                          class="inline-flex h-6 w-6 flex-shrink-0 items-center justify-center rounded text-gray-400 transition-colors hover:bg-gray-200/70 hover:text-gray-700 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:hover:bg-dark-700 dark:hover:text-gray-200"
                          :aria-expanded="expandedIds.has(row.id)"
                          :aria-label="
                            expandedIds.has(row.id)
                              ? t('admin.scheduledTests.logs.collapse')
                              : t('admin.scheduledTests.logs.expand')
                          "
                          :title="
                            expandedIds.has(row.id)
                              ? t('admin.scheduledTests.logs.collapse')
                              : t('admin.scheduledTests.logs.expand')
                          "
                          :data-testid="`detection-log-expand-${row.id}`"
                          @click.stop="toggleExpand(row.id)"
                        >
                          <Icon
                            name="chevronDown"
                            size="sm"
                            :class="[
                              'transition-transform duration-200',
                              expandedIds.has(row.id) ? 'rotate-180' : '-rotate-90'
                            ]"
                          />
                        </button>
                        <span>{{ formatDateTime(row.started_at || row.created_at) }}</span>
                      </div>
                    </td>

                    <!-- Account (name + #ID) -->
                    <td class="whitespace-nowrap px-4 py-4 text-sm">
                      <div class="min-w-0 max-w-[220px]">
                        <div
                          class="truncate font-medium text-gray-900 dark:text-white"
                          :title="row.account_name || `#${row.account_id}`"
                        >
                          {{ row.account_name || `#${row.account_id}` }}
                        </div>
                        <div class="mt-0.5 font-mono text-xs text-gray-400 dark:text-gray-500">
                          #{{ row.account_id }}
                        </div>
                      </div>
                    </td>

                    <!-- Platform -->
                    <td class="whitespace-nowrap px-4 py-4 font-mono text-sm text-gray-700 dark:text-gray-300">
                      {{ row.platform || '—' }}
                    </td>

                    <!-- Test Type Badge -->
                    <td class="whitespace-nowrap px-4 py-4 text-sm">
                      <span
                        :class="[
                          'inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium',
                          getTestTypeBadgeClass(resolveRowTestType(row))
                        ]"
                      >
                        {{ getTestTypeLabel(resolveRowTestType(row)) }}
                      </span>
                    </td>

                    <!-- Request Model -->
                    <td class="whitespace-nowrap px-4 py-4 font-mono text-sm text-gray-800 dark:text-gray-200">
                      {{ row.model_id || row.diagnostics?.model || '—' }}
                    </td>

                    <!-- Status Badge -->
                    <td class="whitespace-nowrap px-4 py-4 text-sm">
                      <span
                        :class="[
                          'inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium',
                          getResultStatusBadgeClass(row.status)
                        ]"
                      >
                        {{ getResultStatusLabel(row.status) }}
                      </span>
                    </td>

                    <!-- Fingerprint (prediction · expected · probability · badge) -->
                    <td class="whitespace-nowrap px-4 py-4 text-sm">
                      <div
                        v-if="row.diagnostics?.fingerprint"
                        class="flex flex-wrap items-center gap-1.5 text-xs"
                      >
                        <span class="font-mono font-semibold text-gray-900 dark:text-gray-100">
                          {{ row.diagnostics.fingerprint.prediction || '-' }}
                        </span>
                        <template v-if="resolveExpectedModel(row)">
                          <span class="text-gray-300 dark:text-gray-600">·</span>
                          <span class="text-gray-500 dark:text-gray-400">
                            {{ t('admin.scheduledTests.expectedModelLabel') }}:
                            <span class="font-mono text-gray-700 dark:text-gray-300">
                              {{ resolveExpectedModel(row) }}
                            </span>
                          </span>
                        </template>
                        <span class="text-gray-300 dark:text-gray-600">·</span>
                        <span class="font-mono text-gray-600 dark:text-gray-300">
                          {{ formatProbability(row.diagnostics.fingerprint.probability) }}
                        </span>
                        <span
                          :class="[
                            'inline-flex items-center rounded-full px-2 py-0.5 text-[11px] font-medium',
                            getFingerprintStatusBadgeClass(row.diagnostics.fingerprint.status)
                          ]"
                        >
                          {{ getFingerprintStatusLabel(row.diagnostics.fingerprint.status) }}
                        </span>
                      </div>
                      <span v-else class="text-gray-400 dark:text-gray-500">—</span>
                    </td>

                    <!-- Quality (score / threshold) -->
                    <td class="whitespace-nowrap px-4 py-4 text-sm">
                      <div
                        v-if="row.diagnostics?.quality"
                        class="flex items-center gap-1.5 text-xs"
                      >
                        <span class="font-mono font-semibold text-gray-900 dark:text-gray-100">
                          {{ row.diagnostics.quality.score }}/{{ row.diagnostics.quality.threshold }}
                        </span>
                        <span
                          :class="[
                            'inline-flex items-center rounded-full px-2 py-0.5 text-[11px] font-medium',
                            getQualityStatusBadgeClass(row.diagnostics.quality.status)
                          ]"
                        >
                          {{ getQualityStatusLabel(row.diagnostics.quality.status) }}
                        </span>
                      </div>
                      <span v-else class="text-gray-400 dark:text-gray-500">—</span>
                    </td>

                    <!-- Latency -->
                    <td class="whitespace-nowrap px-4 py-4 font-mono text-sm text-gray-500 dark:text-gray-400">
                      {{ row.latency_ms > 0 ? `${row.latency_ms} ms` : '—' }}
                    </td>
                  </tr>

                  <!-- Expanded Row Detail -->
                  <tr
                    v-if="expandedIds.has(row.id)"
                    :data-testid="`detection-log-detail-${row.id}`"
                    class="bg-gray-50/60 dark:bg-dark-900/50"
                  >
                    <td :colspan="columns.length" class="!whitespace-normal px-6 py-4">
                      <div class="space-y-3">
                        <ScheduledTestDiagnosticsCards
                          v-if="row.diagnostics && (row.diagnostics.fingerprint || row.diagnostics.quality)"
                          :diagnostics="resolveDiagnosticsReport(row)!"
                        />

                        <div v-if="row.error_message" class="space-y-1">
                          <div
                            :class="[
                              'text-xs font-medium',
                              row.status === 'warning'
                                ? 'text-amber-700 dark:text-amber-400'
                                : 'text-red-600 dark:text-red-400'
                            ]"
                          >
                            {{ t('admin.scheduledTests.errorMessage') }}
                          </div>
                          <pre
                            :class="[
                              'max-h-40 overflow-auto whitespace-pre-wrap rounded-lg p-2.5 font-mono text-xs',
                              row.status === 'warning'
                                ? 'bg-amber-50 text-amber-800 dark:bg-amber-900/20 dark:text-amber-300'
                                : 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300'
                            ]"
                          >{{ row.error_message }}</pre>
                        </div>

                        <div
                          v-if="
                            !row.error_message &&
                            (!row.diagnostics || (!row.diagnostics.fingerprint && !row.diagnostics.quality))
                          "
                          class="text-xs text-gray-500 dark:text-gray-400"
                        >
                          {{ t('admin.scheduledTests.noResults') }}
                        </div>
                      </div>
                    </td>
                  </tr>
                </template>
              </template>
            </tbody>
          </table>
        </div>
      </template>

      <!-- Pagination -->
      <template #pagination>
        <Pagination
          v-if="total > 0"
          :total="total"
          :page="page"
          :page-size="pageSize"
          @update:page="onPageChange"
          @update:pageSize="onPageSizeChange"
        />
      </template>
    </TablePageLayout>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import type { Column } from '@/components/common/types'
import Pagination from '@/components/common/Pagination.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import ScheduledTestDiagnosticsCards from '@/components/admin/account/ScheduledTestDiagnosticsCards.vue'
import { useScheduledTestDisplay } from '@/components/admin/account/scheduledTestDisplay'
import { formatDateTime } from '@/utils/format'
import { useAppStore } from '@/stores'
import type { ModelDetectionReport, ScheduledTestLogItem, ScheduledTestLogQuery, ScheduledTestType } from '@/types'

const { t } = useI18n()
const appStore = useAppStore()
const {
  getTestTypeLabel,
  getTestTypeBadgeClass,
  getResultStatusLabel,
  getResultStatusBadgeClass,
  getFingerprintStatusLabel,
  getFingerprintStatusBadgeClass,
  getQualityStatusLabel,
  getQualityStatusBadgeClass,
  formatProbability
} = useScheduledTestDisplay()

const PLATFORMS = ['anthropic', 'openai', 'gemini', 'antigravity', 'grok'] as const
type LogStatusFilter = '' | NonNullable<ScheduledTestLogQuery['status']>
type LogTestTypeFilter = '' | ScheduledTestType
type LogFingerprintStatusFilter = '' | NonNullable<ScheduledTestLogQuery['fingerprint_status']>

const loading = ref(false)
const logs = ref<ScheduledTestLogItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const expandedIds = reactive(new Set<number>())

let logsRequestId = 0

onBeforeUnmount(() => {
  logsRequestId++
})

const filters = reactive({
  platform: '' as string,
  status: 'abnormal' as LogStatusFilter,
  test_type: '' as LogTestTypeFilter,
  fingerprint_status: '' as LogFingerprintStatusFilter,
  account_id: '' as string | number,
  start_time: '' as string,
  end_time: '' as string
})

const platformOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('admin.scheduledTests.logs.all') },
  ...PLATFORMS.map((platform) => ({ value: platform, label: platform }))
])

const statusOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('admin.scheduledTests.logs.all') },
  { value: 'abnormal', label: t('admin.scheduledTests.logs.abnormal') },
  { value: 'success', label: t('admin.scheduledTests.success') },
  { value: 'warning', label: t('admin.scheduledTests.warning') },
  { value: 'failed', label: t('admin.scheduledTests.failed') },
  { value: 'running', label: t('admin.scheduledTests.running') }
])

const testTypeOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('admin.scheduledTests.logs.all') },
  { value: 'connectivity', label: t('admin.scheduledTests.testTypeConnectivity') },
  { value: 'fingerprint', label: t('admin.scheduledTests.testTypeFingerprint') },
  { value: 'quality', label: t('admin.scheduledTests.testTypeQuality') },
  { value: 'comprehensive', label: t('admin.scheduledTests.testTypeComprehensive') }
])

const fingerprintStatusOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('admin.scheduledTests.logs.all') },
  { value: 'match', label: t('admin.scheduledTests.fingerprintMatch') },
  { value: 'mismatch', label: t('admin.scheduledTests.fingerprintMismatch') },
  { value: 'inconclusive', label: t('admin.scheduledTests.fingerprintInconclusive') },
  { value: 'observed', label: t('admin.scheduledTests.fingerprintObserved') }
])

const columns = computed<Column[]>(() => [
  { key: 'started_at', label: t('admin.scheduledTests.logs.time') },
  { key: 'account', label: t('admin.scheduledTests.logs.account') },
  { key: 'platform', label: t('admin.scheduledTests.logs.platform') },
  { key: 'test_type', label: t('admin.scheduledTests.logs.testType') },
  { key: 'model_id', label: t('admin.scheduledTests.logs.model') },
  { key: 'status', label: t('admin.scheduledTests.logs.status') },
  { key: 'fingerprint', label: t('admin.scheduledTests.logs.fingerprint') },
  { key: 'quality', label: t('admin.scheduledTests.logs.quality') },
  { key: 'latency_ms', label: t('admin.scheduledTests.logs.latency') }
])

function resolveRowTestType(row: ScheduledTestLogItem): string {
  return row.diagnostics?.test_type || row.test_type || 'connectivity'
}

function resolveExpectedModel(row: ScheduledTestLogItem): string {
  return row.diagnostics?.expected_model || row.expected_model || ''
}

function resolveDiagnosticsReport(row: ScheduledTestLogItem): ModelDetectionReport | null {
  if (!row.diagnostics) return null
  if (row.diagnostics.expected_model || !row.expected_model) {
    return row.diagnostics
  }
  return {
    ...row.diagnostics,
    expected_model: row.expected_model
  }
}

function buildQuery(targetPage = page.value): ScheduledTestLogQuery | null {
  const rawAccountId = String(filters.account_id ?? '').trim()
  let accountId: number | undefined
  if (rawAccountId !== '') {
    const parsed = Number(rawAccountId)
    if (!/^[1-9]\d*$/.test(rawAccountId) || !Number.isSafeInteger(parsed)) {
      appStore.showError(t('admin.scheduledTests.logs.invalidAccountId'))
      return null
    }
    accountId = parsed
  }

  const rawStart = filters.start_time.trim()
  const rawEnd = filters.end_time.trim()
  let startTime: string | undefined
  let endTime: string | undefined

  if (rawStart !== '') {
    const parsedStart = new Date(rawStart)
    if (Number.isNaN(parsedStart.getTime())) {
      appStore.showError(t('admin.scheduledTests.logs.invalidTimeRange'))
      return null
    }
    startTime = parsedStart.toISOString()
  }

  if (rawEnd !== '') {
    const parsedEnd = new Date(rawEnd)
    if (Number.isNaN(parsedEnd.getTime())) {
      appStore.showError(t('admin.scheduledTests.logs.invalidTimeRange'))
      return null
    }
    endTime = parsedEnd.toISOString()
  }

  if (startTime && endTime && new Date(endTime).getTime() <= new Date(startTime).getTime()) {
    appStore.showError(t('admin.scheduledTests.logs.invalidTimeRange'))
    return null
  }

  return {
    page: targetPage,
    page_size: pageSize.value,
    platform: filters.platform || undefined,
    status: filters.status || undefined,
    test_type: filters.test_type || undefined,
    fingerprint_status: filters.fingerprint_status || undefined,
    account_id: accountId,
    start_time: startTime,
    end_time: endTime
  }
}

async function fetchLogs(targetPage = page.value) {
  page.value = targetPage
  logsRequestId++
  const requestId = logsRequestId
  const query = buildQuery(targetPage)
  if (!query) {
    loading.value = false
    return
  }

  loading.value = true

  try {
    const res = await adminAPI.scheduledTests.listLogs(query)
    if (requestId !== logsRequestId) return
    logs.value = res.items
    total.value = res.total
    expandedIds.clear()
  } catch (err: any) {
    if (requestId !== logsRequestId) return
    appStore.showError(err?.message || t('admin.scheduledTests.logs.loadFailed'))
  } finally {
    if (requestId === logsRequestId) {
      loading.value = false
    }
  }
}

function handleFilterChange() {
  void fetchLogs(1)
}

function resetFilters() {
  filters.platform = ''
  filters.status = 'abnormal'
  filters.test_type = ''
  filters.fingerprint_status = ''
  filters.account_id = ''
  filters.start_time = ''
  filters.end_time = ''
  void fetchLogs(1)
}

function onPageChange(nextPage: number) {
  void fetchLogs(nextPage)
}

function onPageSizeChange(nextPageSize: number) {
  pageSize.value = nextPageSize
  void fetchLogs(1)
}

function toggleExpand(id: number) {
  if (expandedIds.has(id)) {
    expandedIds.delete(id)
  } else {
    expandedIds.add(id)
  }
}

onMounted(() => {
  void fetchLogs(1)
})
</script>
