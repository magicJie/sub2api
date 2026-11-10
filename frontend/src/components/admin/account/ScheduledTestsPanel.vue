<template>
  <BaseDialog
    :show="show"
    :title="t('admin.scheduledTests.title')"
    width="wide"
    @close="emit('close')"
  >
    <div class="space-y-4">
      <!-- Top Bar -->
      <div class="flex items-center justify-between gap-3">
        <p class="text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.scheduledTests.subtitle') }}
        </p>
        <button
          @click="showAddForm = !showAddForm"
          class="btn btn-primary flex shrink-0 items-center gap-1.5 text-sm"
        >
          <Icon name="plus" size="sm" :stroke-width="2" />
          {{ t('admin.scheduledTests.addPlan') }}
        </button>
      </div>

      <!-- Add Plan Form -->
      <div
        v-if="showAddForm"
        class="rounded-xl border border-primary-200 bg-primary-50/50 p-4 dark:border-primary-800 dark:bg-primary-900/20"
      >
        <div class="mb-3 text-sm font-medium text-gray-700 dark:text-gray-300">
          {{ t('admin.scheduledTests.addPlan') }}
        </div>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.scheduledTests.testType') }}
            </label>
            <Select
              :model-value="newPlan.test_type"
              :options="testTypeOptions"
              :searchable="false"
              @update:model-value="(val) => handleNewPlanTestTypeChange(String(val || 'connectivity'))"
            />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.scheduledTests.model') }}
            </label>
            <Select
              v-model="newPlan.model_id"
              :options="modelOptions"
              :placeholder="t('admin.scheduledTests.model')"
              :searchable="true"
              :creatable="true"
            />
          </div>
          <div v-if="includesFingerprint(newPlan.test_type)">
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.scheduledTests.expectedModel') }}
            </label>
            <Input
              v-model="newPlan.expected_model"
              :placeholder="t('admin.scheduledTests.expectedModelPlaceholder')"
              :hint="t('admin.scheduledTests.expectedModelHelp')"
            />
          </div>
          <div v-if="includesQuality(newPlan.test_type)">
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.scheduledTests.qualityThreshold') }}
            </label>
            <Input
              v-model="newPlan.quality_threshold"
              type="number"
              placeholder="80"
              :hint="t('admin.scheduledTests.qualityThresholdHelp')"
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
              v-model="newPlan.cron_expression"
              :placeholder="'*/30 * * * *'"
              :hint="t('admin.scheduledTests.cronHelp')"
            />
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
              v-model="newPlan.max_results"
              type="number"
              placeholder="100"
            />
          </div>
          <div class="flex items-end">
            <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
              <Toggle v-model="newPlan.enabled" />
              {{ t('admin.scheduledTests.enabled') }}
            </label>
          </div>
          <div class="flex items-end">
            <div>
              <label
                :class="[
                  'flex items-center gap-2 text-sm',
                  isConnectivityType(newPlan.test_type)
                    ? 'text-gray-700 dark:text-gray-300'
                    : 'text-gray-400 dark:text-gray-500'
                ]"
              >
                <Toggle
                  v-model="newPlan.auto_recover"
                  :disabled="!isConnectivityType(newPlan.test_type)"
                />
                {{ t('admin.scheduledTests.autoRecover') }}
              </label>
              <p class="mt-0.5 text-xs text-gray-400 dark:text-gray-500">
                {{
                  isConnectivityType(newPlan.test_type)
                    ? t('admin.scheduledTests.autoRecoverHelp')
                    : t('admin.scheduledTests.autoRecoverConnectivityOnly')
                }}
              </p>
            </div>
          </div>
        </div>

        <!-- Upstream Cost & Limitation Notice -->
        <div class="mt-3 rounded-lg border border-amber-200/80 bg-amber-50/70 p-2.5 text-xs text-amber-800 dark:border-amber-800/60 dark:bg-amber-900/20 dark:text-amber-300">
          <div class="font-medium">{{ t('admin.scheduledTests.costNoticeTitle') }}</div>
          <p class="mt-0.5">{{ getCostNoticeText(newPlan.test_type) }}</p>
          <p v-if="includesFingerprint(newPlan.test_type)" class="mt-1 text-amber-700/90 dark:text-amber-300/90">
            {{ t('admin.scheduledTests.closedSetLimitation') }}
          </p>
        </div>

        <div class="mt-3 flex justify-end gap-2">
          <button
            @click="showAddForm = false; resetNewPlan()"
            class="rounded-lg bg-gray-100 px-3 py-1.5 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-200 dark:bg-dark-600 dark:text-gray-300 dark:hover:bg-dark-500"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            @click="handleCreate"
            :disabled="!newPlan.model_id || !newPlan.cron_expression || creating"
            class="flex items-center gap-1.5 rounded-lg bg-primary-500 px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-primary-600 disabled:cursor-not-allowed disabled:opacity-50"
          >
            <Icon v-if="creating" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
            {{ t('common.save') }}
          </button>
        </div>
      </div>

      <!-- Loading State -->
      <div v-if="loading" class="flex items-center justify-center py-8">
        <Icon name="refresh" size="md" class="animate-spin text-gray-400" :stroke-width="2" />
        <span class="ml-2 text-sm text-gray-500">{{ t('common.loading') }}...</span>
      </div>

      <!-- Empty State -->
      <div
        v-else-if="plans.length === 0"
        class="rounded-xl border border-dashed border-gray-300 py-10 text-center dark:border-dark-600"
      >
        <Icon name="calendar" size="lg" class="mx-auto mb-2 text-gray-400" :stroke-width="1.5" />
        <p class="text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.scheduledTests.noPlans') }}
        </p>
      </div>

      <!-- Plans List -->
      <div v-else class="space-y-3">
        <div
          v-for="plan in plans"
          :key="plan.id"
          class="rounded-xl border border-gray-200 bg-white transition-all dark:border-dark-600 dark:bg-dark-800"
        >
          <!-- Plan Header -->
          <div
            class="flex cursor-pointer items-center justify-between px-4 py-3"
            @click="toggleExpand(plan.id)"
          >
            <div class="flex flex-1 flex-wrap items-center gap-3">
              <!-- Model & Type -->
              <div class="min-w-0">
                <div class="flex flex-wrap items-center gap-2">
                  <span class="text-sm font-medium text-gray-900 dark:text-gray-100">
                    {{ plan.model_id }}
                  </span>
                  <span
                    :class="[
                      'inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium',
                      getTestTypeBadgeClass(plan.test_type)
                    ]"
                  >
                    {{ getTestTypeLabel(plan.test_type) }}
                  </span>
                </div>
                <div class="mt-0.5 flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
                  <span class="font-mono">{{ plan.cron_expression }}</span>
                  <span
                    v-if="includesFingerprint(plan.test_type) && plan.expected_model"
                    class="font-mono text-gray-500 dark:text-gray-400"
                  >
                    · {{ t('admin.scheduledTests.expectedModelLabel') }}: {{ plan.expected_model }}
                  </span>
                  <span
                    v-if="includesQuality(plan.test_type)"
                    class="font-mono text-gray-500 dark:text-gray-400"
                  >
                    · {{ t('admin.scheduledTests.qualityThresholdLabel') }}: {{ plan.quality_threshold ?? 80 }}
                  </span>
                </div>
              </div>

              <!-- Enabled Toggle -->
              <div class="flex items-center gap-1.5" @click.stop>
                <Toggle
                  :model-value="plan.enabled"
                  @update:model-value="(val: boolean) => handleToggleEnabled(plan, val)"
                />
                <span class="text-xs text-gray-500 dark:text-gray-400">
                  {{ plan.enabled ? t('admin.scheduledTests.enabled') : '' }}
                </span>
              </div>

              <!-- Auto Recover Badge -->
              <span
                v-if="plan.auto_recover && isConnectivityType(plan.test_type)"
                class="inline-flex items-center rounded-full bg-emerald-100 px-2 py-0.5 text-xs font-medium text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-400"
              >
                {{ t('admin.scheduledTests.autoRecover') }}
              </span>
            </div>

            <div class="flex items-center gap-3">
              <!-- Last Run -->
              <div v-if="plan.last_run_at" class="hidden text-right text-xs text-gray-500 dark:text-gray-400 sm:block">
                <div>{{ t('admin.scheduledTests.lastRun') }}</div>
                <div>{{ formatDateTime(plan.last_run_at) }}</div>
              </div>

              <!-- Next Run -->
              <div v-if="plan.next_run_at" class="hidden text-right text-xs text-gray-500 dark:text-gray-400 sm:block">
                <div>{{ t('admin.scheduledTests.nextRun') }}</div>
                <div>{{ formatDateTime(plan.next_run_at) }}</div>
              </div>

              <!-- Actions -->
              <div class="flex items-center gap-1" @click.stop>
                <button
                  data-testid="run-plan-btn"
                  @click="handleRunNow(plan)"
                  :disabled="runningPlanIds.has(plan.id)"
                  class="inline-flex items-center gap-1 rounded-lg px-2 py-1.5 text-xs font-medium text-emerald-600 transition-colors hover:bg-emerald-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-emerald-400 dark:hover:bg-emerald-900/20"
                  :title="runningPlanIds.has(plan.id) ? t('admin.scheduledTests.runNowRunning') : t('admin.scheduledTests.runNow')"
                >
                  <Icon
                    :name="runningPlanIds.has(plan.id) ? 'refresh' : 'play'"
                    size="sm"
                    :class="runningPlanIds.has(plan.id) ? 'animate-spin' : ''"
                    :stroke-width="2"
                  />
                  <span class="hidden sm:inline">
                    {{ runningPlanIds.has(plan.id) ? t('admin.scheduledTests.runNowRunning') : t('admin.scheduledTests.runNow') }}
                  </span>
                </button>
                <button
                  @click="startEdit(plan)"
                  class="rounded-lg p-1.5 text-gray-400 transition-colors hover:bg-blue-50 hover:text-blue-500 dark:hover:bg-blue-900/20"
                  :title="t('admin.scheduledTests.editPlan')"
                >
                  <Icon name="edit" size="sm" :stroke-width="2" />
                </button>
                <button
                  @click="confirmDeletePlan(plan)"
                  class="rounded-lg p-1.5 text-gray-400 transition-colors hover:bg-red-50 hover:text-red-500 dark:hover:bg-red-900/20"
                  :title="t('admin.scheduledTests.deletePlan')"
                >
                  <Icon name="trash" size="sm" :stroke-width="2" />
                </button>
              </div>

              <!-- Expand indicator -->
              <Icon
                name="chevronDown"
                size="sm"
                :class="[
                  'text-gray-400 transition-transform duration-200',
                  expandedPlanId === plan.id ? 'rotate-180' : ''
                ]"
              />
            </div>
          </div>

          <!-- Edit Form -->
          <div
            v-if="editingPlanId === plan.id"
            class="border-t border-blue-100 bg-blue-50/50 px-4 py-3 dark:border-blue-900 dark:bg-blue-900/10"
            @click.stop
          >
            <div class="mb-2 text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.scheduledTests.editPlan') }}
            </div>
            <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
                  {{ t('admin.scheduledTests.testType') }}
                </label>
                <Select
                  :model-value="editForm.test_type"
                  :options="testTypeOptions"
                  :searchable="false"
                  @update:model-value="(val) => handleEditFormTestTypeChange(String(val || 'connectivity'))"
                />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
                  {{ t('admin.scheduledTests.model') }}
                </label>
                <Select
                  v-model="editForm.model_id"
                  :options="modelOptions"
                  :placeholder="t('admin.scheduledTests.model')"
                  :searchable="true"
                  :creatable="true"
                />
              </div>
              <div v-if="includesFingerprint(editForm.test_type)">
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
                  {{ t('admin.scheduledTests.expectedModel') }}
                </label>
                <Input
                  v-model="editForm.expected_model"
                  :placeholder="t('admin.scheduledTests.expectedModelPlaceholder')"
                  :hint="t('admin.scheduledTests.expectedModelHelp')"
                />
              </div>
              <div v-if="includesQuality(editForm.test_type)">
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
                  {{ t('admin.scheduledTests.qualityThreshold') }}
                </label>
                <Input
                  v-model="editForm.quality_threshold"
                  type="number"
                  placeholder="80"
                  :hint="t('admin.scheduledTests.qualityThresholdHelp')"
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
                  v-model="editForm.cron_expression"
                  :placeholder="'*/30 * * * *'"
                  :hint="t('admin.scheduledTests.cronHelp')"
                />
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
                  v-model="editForm.max_results"
                  type="number"
                  placeholder="100"
                />
              </div>
              <div class="flex items-end">
                <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
                  <Toggle v-model="editForm.enabled" />
                  {{ t('admin.scheduledTests.enabled') }}
                </label>
              </div>
              <div class="flex items-end">
                <div>
                  <label
                    :class="[
                      'flex items-center gap-2 text-sm',
                      isConnectivityType(editForm.test_type)
                        ? 'text-gray-700 dark:text-gray-300'
                        : 'text-gray-400 dark:text-gray-500'
                    ]"
                  >
                    <Toggle
                      v-model="editForm.auto_recover"
                      :disabled="!isConnectivityType(editForm.test_type)"
                    />
                    {{ t('admin.scheduledTests.autoRecover') }}
                  </label>
                  <p class="mt-0.5 text-xs text-gray-400 dark:text-gray-500">
                    {{
                      isConnectivityType(editForm.test_type)
                        ? t('admin.scheduledTests.autoRecoverHelp')
                        : t('admin.scheduledTests.autoRecoverConnectivityOnly')
                    }}
                  </p>
                </div>
              </div>
            </div>

            <!-- Upstream Cost & Limitation Notice -->
            <div class="mt-3 rounded-lg border border-amber-200/80 bg-amber-50/70 p-2.5 text-xs text-amber-800 dark:border-amber-800/60 dark:bg-amber-900/20 dark:text-amber-300">
              <div class="font-medium">{{ t('admin.scheduledTests.costNoticeTitle') }}</div>
              <p class="mt-0.5">{{ getCostNoticeText(editForm.test_type) }}</p>
              <p v-if="includesFingerprint(editForm.test_type)" class="mt-1 text-amber-700/90 dark:text-amber-300/90">
                {{ t('admin.scheduledTests.closedSetLimitation') }}
              </p>
            </div>

            <div class="mt-3 flex justify-end gap-2">
              <button
                @click="cancelEdit"
                class="rounded-lg bg-gray-100 px-3 py-1.5 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-200 dark:bg-dark-600 dark:text-gray-300 dark:hover:bg-dark-500"
              >
                {{ t('common.cancel') }}
              </button>
              <button
                @click="handleEdit"
                :disabled="!editForm.model_id || !editForm.cron_expression || updating"
                class="flex items-center gap-1.5 rounded-lg bg-primary-500 px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-primary-600 disabled:cursor-not-allowed disabled:opacity-50"
              >
                <Icon v-if="updating" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
                {{ t('common.save') }}
              </button>
            </div>
          </div>

          <!-- Expanded Results Section -->
          <div
            v-if="expandedPlanId === plan.id"
            class="border-t border-gray-100 px-4 py-3 dark:border-dark-700"
          >
            <div class="mb-2 text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.scheduledTests.results') }}
            </div>

            <!-- Results Loading -->
            <div v-if="loadingResults" class="flex items-center justify-center py-4">
              <Icon name="refresh" size="sm" class="animate-spin text-gray-400" :stroke-width="2" />
              <span class="ml-2 text-xs text-gray-500">{{ t('common.loading') }}...</span>
            </div>

            <!-- No Results -->
            <div
              v-else-if="results.length === 0"
              class="py-4 text-center text-xs text-gray-500 dark:text-gray-400"
            >
              {{ t('admin.scheduledTests.noResults') }}
            </div>

            <!-- Results List -->
            <div v-else class="max-h-96 space-y-2.5 overflow-y-auto">
              <div
                v-for="result in results"
                :key="result.id"
                :class="[
                  'rounded-lg border p-3',
                  result.status === 'warning'
                    ? 'border-amber-200/80 bg-amber-50/30 dark:border-amber-800/50 dark:bg-amber-900/10'
                    : 'border-gray-100 bg-gray-50 dark:border-dark-700 dark:bg-dark-900'
                ]"
              >
                <div class="flex items-center justify-between">
                  <div class="flex flex-wrap items-center gap-2">
                    <!-- Status Badge -->
                    <span
                      :class="[
                        'inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium',
                        getResultStatusBadgeClass(result.status)
                      ]"
                    >
                      {{ getResultStatusLabel(result.status) }}
                    </span>

                    <!-- Diagnostic Test Type Badge -->
                    <span
                      v-if="result.diagnostics?.test_type"
                      :class="[
                        'inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium',
                        getTestTypeBadgeClass(result.diagnostics.test_type)
                      ]"
                    >
                      {{ getTestTypeLabel(result.diagnostics.test_type) }}
                    </span>

                    <!-- Latency -->
                    <span v-if="result.latency_ms > 0" class="text-xs text-gray-500 dark:text-gray-400">
                      {{ result.latency_ms }}ms
                    </span>
                  </div>

                  <!-- Started At -->
                  <span class="text-xs text-gray-400">
                    {{ formatDateTime(result.started_at) }}
                  </span>
                </div>

                <ScheduledTestDiagnosticsCards v-if="result.diagnostics" :diagnostics="result.diagnostics" />

                <!-- Response / Error (collapsible) -->
                <div v-if="result.error_message" class="mt-2">
                  <div
                    :class="[
                      'cursor-pointer text-xs font-medium',
                      result.status === 'warning'
                        ? 'text-amber-700 dark:text-amber-400'
                        : 'text-red-600 dark:text-red-400'
                    ]"
                    @click="toggleResultDetail(result.id)"
                  >
                    {{ t('admin.scheduledTests.errorMessage') }}
                    <Icon
                      name="chevronDown"
                      size="sm"
                      :class="[
                        'inline transition-transform duration-200',
                        expandedResultIds.has(result.id) ? 'rotate-180' : ''
                      ]"
                    />
                  </div>
                  <pre
                    v-if="expandedResultIds.has(result.id)"
                    :class="[
                      'mt-1 max-h-32 overflow-auto whitespace-pre-wrap rounded p-2 text-xs',
                      result.status === 'warning'
                        ? 'bg-amber-50 text-amber-800 dark:bg-amber-900/20 dark:text-amber-300'
                        : 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300'
                    ]"
                  >{{ result.error_message }}</pre>
                </div>
                <div v-else-if="result.response_text" class="mt-2">
                  <div
                    class="cursor-pointer text-xs font-medium text-gray-600 dark:text-gray-400"
                    @click="toggleResultDetail(result.id)"
                  >
                    {{ t('admin.scheduledTests.responseText') }}
                    <Icon
                      name="chevronDown"
                      size="sm"
                      :class="[
                        'inline transition-transform duration-200',
                        expandedResultIds.has(result.id) ? 'rotate-180' : ''
                      ]"
                    />
                  </div>
                  <pre
                    v-if="expandedResultIds.has(result.id)"
                    class="mt-1 max-h-32 overflow-auto whitespace-pre-wrap rounded bg-gray-100 p-2 text-xs text-gray-700 dark:bg-dark-800 dark:text-gray-300"
                  >{{ result.response_text }}</pre>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Delete Confirmation -->
    <ConfirmDialog
      :show="showDeleteConfirm"
      :title="t('admin.scheduledTests.deletePlan')"
      :message="t('admin.scheduledTests.confirmDelete')"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="handleDelete"
      @cancel="showDeleteConfirm = false"
    />
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, reactive, watch, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import Input from '@/components/common/Input.vue'
import Toggle from '@/components/common/Toggle.vue'
import { Icon } from '@/components/icons'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import { DEFAULT_FINGERPRINT_CRON, resolveDetectionDefaults } from '@/utils/scheduledTestDefaults'
import ScheduledTestDiagnosticsCards from './ScheduledTestDiagnosticsCards.vue'
import { useScheduledTestDisplay } from './scheduledTestDisplay'
import type { ScheduledTestPlan, ScheduledTestResult, ScheduledTestType } from '@/types'

const { t } = useI18n()
const {
  getTestTypeLabel, getTestTypeBadgeClass, getResultStatusLabel, getResultStatusBadgeClass
} = useScheduledTestDisplay()
const appStore = useAppStore()

const props = defineProps<{
  show: boolean
  accountId: number | null
  platform?: string
  modelOptions: SelectOption[]
}>()

const emit = defineEmits<{
  (e: 'close'): void
}>()

// State
const loading = ref(false)
const creating = ref(false)
const loadingResults = ref(false)
const plans = ref<ScheduledTestPlan[]>([])
const results = ref<ScheduledTestResult[]>([])
const expandedPlanId = ref<number | null>(null)
const runningPlanIds = reactive(new Set<number>())
let panelSessionId = 0
let resultsRequestId = 0

watch(expandedPlanId, () => {
  resultsRequestId++
  results.value = []
  loadingResults.value = false
}, { flush: 'sync' })

onBeforeUnmount(() => {
  panelSessionId++
  resultsRequestId++
})

const expandedResultIds = reactive(new Set<number>())
const showAddForm = ref(false)
const showDeleteConfirm = ref(false)
const deletingPlan = ref<ScheduledTestPlan | null>(null)
const editingPlanId = ref<number | null>(null)
const updating = ref(false)

const testTypeOptions = computed<SelectOption[]>(() => [
  { value: 'connectivity', label: t('admin.scheduledTests.testTypeConnectivity') },
  { value: 'fingerprint', label: t('admin.scheduledTests.testTypeFingerprint') },
  { value: 'quality', label: t('admin.scheduledTests.testTypeQuality') },
  { value: 'comprehensive', label: t('admin.scheduledTests.testTypeComprehensive') }
])

const normalizeTestType = (type?: string | null): ScheduledTestType => {
  if (type === 'fingerprint' || type === 'quality' || type === 'comprehensive') {
    return type
  }
  return 'connectivity'
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

const getCostNoticeText = (type?: string | null): string => {
  const normalized = normalizeTestType(type)
  if (normalized === 'fingerprint') return t('admin.scheduledTests.costNoticeFingerprint')
  if (normalized === 'quality') return t('admin.scheduledTests.costNoticeQuality')
  if (normalized === 'comprehensive') return t('admin.scheduledTests.costNoticeComprehensive')
  return t('admin.scheduledTests.costNoticeConnectivity')
}

const editForm = reactive({
  test_type: 'connectivity' as ScheduledTestType,
  model_id: '' as string,
  expected_model: '' as string,
  quality_threshold: '80' as string,
  cron_expression: '' as string,
  max_results: '100' as string,
  enabled: true,
  auto_recover: false
})

const newPlan = reactive({
  test_type: 'fingerprint' as ScheduledTestType,
  ...resolveDetectionDefaults(props.platform ? [props.platform] : []),
  quality_threshold: '80' as string,
  cron_expression: DEFAULT_FINGERPRINT_CRON,
  max_results: '100' as string,
  enabled: true,
  auto_recover: false
})

const handleNewPlanTestTypeChange = (type: string) => {
  const normalized = normalizeTestType(type)
  newPlan.test_type = normalized
  if (normalized !== 'connectivity') {
    newPlan.auto_recover = false
  }
}

const handleEditFormTestTypeChange = (type: string) => {
  const normalized = normalizeTestType(type)
  editForm.test_type = normalized
  if (normalized !== 'connectivity') {
    editForm.auto_recover = false
  }
}

const resetNewPlan = () => {
  const defaults = resolveDetectionDefaults(props.platform ? [props.platform] : [])
  newPlan.test_type = 'fingerprint'
  newPlan.model_id = defaults.model_id
  newPlan.expected_model = defaults.expected_model
  newPlan.quality_threshold = '80'
  newPlan.cron_expression = DEFAULT_FINGERPRINT_CRON
  newPlan.max_results = '100'
  newPlan.enabled = true
  newPlan.auto_recover = false
}

// Load plans when dialog opens or account switches
watch(
  [() => props.show, () => props.accountId],
  async ([visible, accountId]) => {
    panelSessionId++
    resultsRequestId++
    plans.value = []
    results.value = []
    loading.value = false
    loadingResults.value = false
    creating.value = false
    updating.value = false
    expandedPlanId.value = null
    expandedResultIds.clear()
    runningPlanIds.clear()
    editingPlanId.value = null
    showAddForm.value = false
    showDeleteConfirm.value = false
    deletingPlan.value = null
    resetNewPlan()
    if (visible && accountId) {
      await loadPlans()
    }
  }
)

const loadPlans = async () => {
  if (!props.accountId) return
  const sessionId = panelSessionId
  loading.value = true
  try {
    const data = await adminAPI.scheduledTests.listByAccount(props.accountId)
    if (sessionId !== panelSessionId) return
    plans.value = data
  } catch (error: any) {
    if (sessionId !== panelSessionId) return
    appStore.showError(error?.message || 'Failed to load plans')
  } finally {
    if (sessionId === panelSessionId) {
      loading.value = false
    }
  }
}

const handleCreate = async () => {
  if (!props.accountId || !newPlan.model_id || !newPlan.cron_expression) return
  const sessionId = panelSessionId
  creating.value = true
  try {
    const testType = normalizeTestType(newPlan.test_type)
    await adminAPI.scheduledTests.create({
      account_id: props.accountId,
      model_id: newPlan.model_id,
      test_type: testType,
      expected_model: includesFingerprint(testType) ? newPlan.expected_model.trim() : '',
      quality_threshold: includesQuality(testType)
        ? normalizeQualityThreshold(newPlan.quality_threshold)
        : 80,
      cron_expression: newPlan.cron_expression,
      enabled: newPlan.enabled,
      max_results: normalizeMaxResults(newPlan.max_results),
      auto_recover: testType === 'connectivity' ? newPlan.auto_recover : false
    })
    if (sessionId !== panelSessionId) return
    appStore.showSuccess?.(t('admin.scheduledTests.createSuccess'))
    showAddForm.value = false
    resetNewPlan()
    await loadPlans()
  } catch (error: any) {
    if (sessionId !== panelSessionId) return
    appStore.showError(error?.message || 'Failed to create plan')
  } finally {
    if (sessionId === panelSessionId) {
      creating.value = false
    }
  }
}

const handleToggleEnabled = async (plan: ScheduledTestPlan, enabled: boolean) => {
  const sessionId = panelSessionId
  try {
    const updated = await adminAPI.scheduledTests.update(plan.id, { enabled })
    if (sessionId !== panelSessionId) return
    const index = plans.value.findIndex((p) => p.id === plan.id)
    if (index !== -1) {
      plans.value[index] = updated
    }
    appStore.showSuccess?.(t('admin.scheduledTests.updateSuccess'))
  } catch (error: any) {
    if (sessionId !== panelSessionId) return
    appStore.showError(error?.message || 'Failed to update plan')
  }
}

const startEdit = (plan: ScheduledTestPlan) => {
  const testType = normalizeTestType(plan.test_type)
  editingPlanId.value = plan.id
  editForm.test_type = testType
  editForm.model_id = plan.model_id
  editForm.expected_model = plan.expected_model || ''
  editForm.quality_threshold = String(plan.quality_threshold ?? 80)
  editForm.cron_expression = plan.cron_expression
  editForm.max_results = String(plan.max_results || 100)
  editForm.enabled = plan.enabled
  editForm.auto_recover = testType === 'connectivity' ? plan.auto_recover : false
}

const cancelEdit = () => {
  editingPlanId.value = null
}

const handleEdit = async () => {
  if (!editingPlanId.value || !editForm.model_id || !editForm.cron_expression) return
  const sessionId = panelSessionId
  updating.value = true
  try {
    const testType = normalizeTestType(editForm.test_type)
    const updated = await adminAPI.scheduledTests.update(editingPlanId.value, {
      model_id: editForm.model_id,
      test_type: testType,
      expected_model: includesFingerprint(testType) ? editForm.expected_model.trim() : '',
      quality_threshold: includesQuality(testType)
        ? normalizeQualityThreshold(editForm.quality_threshold)
        : 80,
      cron_expression: editForm.cron_expression,
      max_results: normalizeMaxResults(editForm.max_results),
      enabled: editForm.enabled,
      auto_recover: testType === 'connectivity' ? editForm.auto_recover : false
    })
    if (sessionId !== panelSessionId) return
    const index = plans.value.findIndex((p) => p.id === editingPlanId.value)
    if (index !== -1) {
      plans.value[index] = updated
    }
    appStore.showSuccess?.(t('admin.scheduledTests.updateSuccess'))
    editingPlanId.value = null
  } catch (error: any) {
    if (sessionId !== panelSessionId) return
    appStore.showError(error?.message || 'Failed to update plan')
  } finally {
    if (sessionId === panelSessionId) {
      updating.value = false
    }
  }
}

const confirmDeletePlan = (plan: ScheduledTestPlan) => {
  deletingPlan.value = plan
  showDeleteConfirm.value = true
}

const handleDelete = async () => {
  if (!deletingPlan.value) return
  const sessionId = panelSessionId
  const targetId = deletingPlan.value.id
  try {
    await adminAPI.scheduledTests.delete(targetId)
    if (sessionId !== panelSessionId) return
    appStore.showSuccess?.(t('admin.scheduledTests.deleteSuccess'))
    plans.value = plans.value.filter((p) => p.id !== targetId)
    if (expandedPlanId.value === targetId) {
      expandedPlanId.value = null
      results.value = []
    }
  } catch (error: any) {
    if (sessionId !== panelSessionId) return
    appStore.showError(error?.message || 'Failed to delete plan')
  } finally {
    if (sessionId === panelSessionId) {
      showDeleteConfirm.value = false
      deletingPlan.value = null
    }
  }
}

const fetchPlanResults = async (planId: number, fallbackResult?: ScheduledTestResult) => {
  resultsRequestId++
  const requestId = resultsRequestId
  const sessionId = panelSessionId
  loadingResults.value = true
  try {
    const data = await adminAPI.scheduledTests.listResults(planId, 20)
    if (sessionId !== panelSessionId || requestId !== resultsRequestId) return
    if (Array.isArray(data) && data.length > 0) {
      results.value =
        fallbackResult && !data.some((item) => item.id === fallbackResult.id)
          ? [fallbackResult, ...data]
          : data
    } else if (fallbackResult) {
      results.value = [fallbackResult]
    } else {
      results.value = []
    }
  } catch (error: any) {
    if (sessionId !== panelSessionId || requestId !== resultsRequestId) return
    appStore.showError(error?.message || 'Failed to load results')
    results.value = fallbackResult ? [fallbackResult] : []
  } finally {
    if (sessionId === panelSessionId && requestId === resultsRequestId) {
      loadingResults.value = false
    }
  }
}

const toggleExpand = async (planId: number) => {
  if (expandedPlanId.value === planId) {
    expandedPlanId.value = null
    results.value = []
    expandedResultIds.clear()
    return
  }

  expandedPlanId.value = planId
  expandedResultIds.clear()
  await fetchPlanResults(planId)
}

const handleRunNow = async (plan: ScheduledTestPlan) => {
  if (runningPlanIds.has(plan.id)) return
  const sessionId = panelSessionId
  runningPlanIds.add(plan.id)
  try {
    const executed = await adminAPI.scheduledTests.runNow(plan.id)
    if (sessionId !== panelSessionId) return
    const currentPlan = plans.value.find((p) => p.id === plan.id)
    if (currentPlan && (executed?.finished_at || executed?.started_at)) {
      currentPlan.last_run_at = executed.finished_at || executed.started_at
    }
    appStore.showSuccess?.(t('admin.scheduledTests.runSuccess'))
    if (expandedPlanId.value !== plan.id) {
      expandedPlanId.value = plan.id
      expandedResultIds.clear()
    }
    await fetchPlanResults(plan.id, executed)
  } catch (error: any) {
    if (sessionId !== panelSessionId) return
    appStore.showError(error?.message || t('admin.scheduledTests.runFailed'))
  } finally {
    if (sessionId === panelSessionId) runningPlanIds.delete(plan.id)
  }
}

const toggleResultDetail = (resultId: number) => {
  if (expandedResultIds.has(resultId)) {
    expandedResultIds.delete(resultId)
  } else {
    expandedResultIds.add(resultId)
  }
}
</script>
