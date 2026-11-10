<template>
  <div
    v-if="diagnostics && (diagnostics.fingerprint || diagnostics.quality)"
    class="mt-2.5 space-y-2"
  >
    <!-- Fingerprint Card -->
    <div
      v-if="diagnostics.fingerprint"
      class="rounded-lg border border-gray-200/80 bg-white/80 p-2.5 text-xs dark:border-dark-700 dark:bg-dark-800/80"
    >
      <div class="flex flex-wrap items-center justify-between gap-2">
        <span class="font-medium text-gray-700 dark:text-gray-200">
          {{ t('admin.scheduledTests.fingerprintTitle') }}
        </span>
        <span
          :class="[
            'inline-flex items-center rounded-full px-2 py-0.5 text-[11px] font-medium',
            getFingerprintStatusBadgeClass(diagnostics.fingerprint.status)
          ]"
        >
          {{ getFingerprintStatusLabel(diagnostics.fingerprint.status) }}
        </span>
      </div>

      <div class="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-gray-600 dark:text-gray-300">
        <div>
          <span class="text-gray-400 dark:text-gray-500">{{ t('admin.scheduledTests.predictedModel') }}: </span>
          <span class="font-mono font-semibold text-gray-900 dark:text-gray-100">
            {{ diagnostics.fingerprint.prediction || '-' }}
          </span>
        </div>
        <div v-if="diagnostics.expected_model">
          <span class="text-gray-400 dark:text-gray-500">{{ t('admin.scheduledTests.expectedModelLabel') }}: </span>
          <span class="font-mono">{{ diagnostics.expected_model }}</span>
        </div>
        <div>
          <span class="text-gray-400 dark:text-gray-500">{{ t('admin.scheduledTests.relativeProbability') }}: </span>
          <span class="font-mono font-medium">
            {{ formatProbability(diagnostics.fingerprint.probability) }}
          </span>
        </div>
        <div v-if="diagnostics.fingerprint.used_outputs > 0">
          <span class="text-gray-400 dark:text-gray-500">{{ t('admin.scheduledTests.usedOutputs') }}: </span>
          <span class="font-mono">{{ diagnostics.fingerprint.used_outputs }}</span>
        </div>
      </div>

      <!-- Top Candidates -->
      <div
        v-if="diagnostics.fingerprint.candidates && diagnostics.fingerprint.candidates.length > 0"
        class="mt-2"
      >
        <div class="mb-1 text-[11px] text-gray-400 dark:text-gray-500">
          {{ t('admin.scheduledTests.topCandidates') }}
        </div>
        <div class="flex flex-wrap gap-1.5">
          <span
            v-for="cand in diagnostics.fingerprint.candidates.slice(0, 4)"
            :key="cand.model"
            class="inline-flex items-center gap-1 rounded bg-gray-100 px-2 py-0.5 font-mono text-[11px] text-gray-700 dark:bg-dark-700 dark:text-gray-300"
          >
            <span>{{ cand.model }}</span>
            <span v-if="cand.family" class="text-gray-400 dark:text-gray-500">({{ cand.family }})</span>
            <span class="font-semibold text-primary-600 dark:text-primary-400">
              {{ formatProbability(cand.probability) }}
            </span>
          </span>
        </div>
      </div>

      <!-- Fingerprint Error -->
      <div
        v-if="diagnostics.fingerprint.error"
        class="mt-1.5 rounded bg-amber-50 px-2 py-1 text-[11px] text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
      >
        {{ diagnostics.fingerprint.error }}
      </div>
    </div>

    <!-- Quality / Ability Evaluation Card -->
    <div
      v-if="diagnostics.quality"
      class="rounded-lg border border-gray-200/80 bg-white/80 p-2.5 text-xs dark:border-dark-700 dark:bg-dark-800/80"
    >
      <div class="flex flex-wrap items-center justify-between gap-2">
        <span class="font-medium text-gray-700 dark:text-gray-200">
          {{ t('admin.scheduledTests.qualityTitle') }}
        </span>
        <span
          :class="[
            'inline-flex items-center rounded-full px-2 py-0.5 text-[11px] font-medium',
            getQualityStatusBadgeClass(diagnostics.quality.status)
          ]"
        >
          {{ getQualityStatusLabel(diagnostics.quality.status) }}
        </span>
      </div>

      <div class="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-gray-600 dark:text-gray-300">
        <div>
          <span class="text-gray-400 dark:text-gray-500">{{ t('admin.scheduledTests.qualityScore') }}: </span>
          <span class="font-mono font-semibold text-gray-900 dark:text-gray-100">
            {{ diagnostics.quality.score }}/100
          </span>
        </div>
        <div>
          <span class="text-gray-400 dark:text-gray-500">{{ t('admin.scheduledTests.qualityThresholdLabel') }}: </span>
          <span class="font-mono">{{ diagnostics.quality.threshold }}</span>
        </div>
        <div>
          <span class="text-gray-400 dark:text-gray-500">{{ t('admin.scheduledTests.qualityChecksPassed') }}: </span>
          <span class="font-mono">
            {{ diagnostics.quality.passed }}/{{ diagnostics.quality.total }}
          </span>
        </div>
      </div>

      <!-- Per-check details -->
      <div
        v-if="diagnostics.quality.checks && diagnostics.quality.checks.length > 0"
        class="mt-2 space-y-1"
      >
        <div
          v-for="check in diagnostics.quality.checks"
          :key="check.id"
          class="flex flex-wrap items-center justify-between gap-2 rounded bg-gray-50 px-2 py-1 text-[11px] dark:bg-dark-900/60"
        >
          <div class="flex items-center gap-1.5">
            <span
              :class="[
                'inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium',
                check.passed
                  ? 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-400'
                  : 'bg-amber-100 text-amber-700 dark:bg-amber-500/20 dark:text-amber-400'
              ]"
            >
              {{ check.passed ? t('admin.scheduledTests.checkPassed') : t('admin.scheduledTests.checkFailed') }}
            </span>
            <span class="font-mono font-medium text-gray-700 dark:text-gray-200">{{ check.id }}</span>
          </div>
          <div class="font-mono text-gray-500 dark:text-gray-400">
            <span>{{ t('admin.scheduledTests.checkExpected') }}: {{ check.expected }}</span>
            <span class="mx-1">·</span>
            <span>{{ t('admin.scheduledTests.checkActual') }}: {{ check.actual || '-' }}</span>
          </div>
        </div>
      </div>

      <!-- Quality Error -->
      <div
        v-if="diagnostics.quality.error"
        class="mt-1.5 rounded bg-amber-50 px-2 py-1 text-[11px] text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
      >
        {{ diagnostics.quality.error }}
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { ModelDetectionReport } from '@/types'
import { useScheduledTestDisplay } from './scheduledTestDisplay'

defineProps<{ diagnostics: ModelDetectionReport }>()

const { t } = useI18n()
const {
  getFingerprintStatusLabel, getFingerprintStatusBadgeClass,
  getQualityStatusLabel, getQualityStatusBadgeClass, formatProbability
} = useScheduledTestDisplay()
</script>
