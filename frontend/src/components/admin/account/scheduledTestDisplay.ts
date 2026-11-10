import { useI18n } from 'vue-i18n'

export function useScheduledTestDisplay() {
  const { t } = useI18n()

  const getTestTypeLabel = (type?: string | null): string => {
    if (type === 'fingerprint') return t('admin.scheduledTests.testTypeFingerprint')
    if (type === 'quality') return t('admin.scheduledTests.testTypeQuality')
    if (type === 'comprehensive') return t('admin.scheduledTests.testTypeComprehensive')
    return t('admin.scheduledTests.testTypeConnectivity')
  }

  const getTestTypeBadgeClass = (type?: string | null): string => {
    if (type === 'fingerprint') {
      return 'bg-purple-100 text-purple-700 dark:bg-purple-500/20 dark:text-purple-300'
    }
    if (type === 'quality') {
      return 'bg-sky-100 text-sky-700 dark:bg-sky-500/20 dark:text-sky-300'
    }
    if (type === 'comprehensive') {
      return 'bg-indigo-100 text-indigo-700 dark:bg-indigo-500/20 dark:text-indigo-300'
    }
    return 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300'
  }

  const getResultStatusLabel = (status: string): string => {
    if (status === 'success') return t('admin.scheduledTests.success')
    if (status === 'warning') return t('admin.scheduledTests.warning')
    if (status === 'running') return t('admin.scheduledTests.running')
    return t('admin.scheduledTests.failed')
  }

  const getResultStatusBadgeClass = (status: string): string => {
    if (status === 'success') {
      return 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-400'
    }
    if (status === 'warning') {
      return 'bg-amber-100 text-amber-700 dark:bg-amber-500/20 dark:text-amber-400'
    }
    if (status === 'running') {
      return 'bg-blue-100 text-blue-700 dark:bg-blue-500/20 dark:text-blue-400'
    }
    return 'bg-red-100 text-red-700 dark:bg-red-500/20 dark:text-red-400'
  }

  const getFingerprintStatusLabel = (status?: string): string => {
    if (status === 'match') return t('admin.scheduledTests.fingerprintMatch')
    if (status === 'mismatch') return t('admin.scheduledTests.fingerprintMismatch')
    if (status === 'observed') return t('admin.scheduledTests.fingerprintObserved')
    return t('admin.scheduledTests.fingerprintInconclusive')
  }

  const getFingerprintStatusBadgeClass = (status?: string): string => {
    if (status === 'match') {
      return 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-400'
    }
    if (status === 'observed') {
      return 'bg-sky-100 text-sky-700 dark:bg-sky-500/20 dark:text-sky-400'
    }
    return 'bg-amber-100 text-amber-700 dark:bg-amber-500/20 dark:text-amber-400'
  }

  const getQualityStatusLabel = (status?: string): string => {
    if (status === 'passed') return t('admin.scheduledTests.qualityPassed')
    if (status === 'degraded') return t('admin.scheduledTests.qualityDegraded')
    return t('admin.scheduledTests.qualityInconclusive')
  }

  const getQualityStatusBadgeClass = (status?: string): string => {
    if (status === 'passed') {
      return 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-400'
    }
    return 'bg-amber-100 text-amber-700 dark:bg-amber-500/20 dark:text-amber-400'
  }

  const formatProbability = (prob?: number): string => {
    const num = Number(prob)
    if (!Number.isFinite(num) || num < 0) return '0.0%'
    return `${(num * 100).toFixed(1)}%`
  }

  return {
    getTestTypeLabel, getTestTypeBadgeClass,
    getResultStatusLabel, getResultStatusBadgeClass,
    getFingerprintStatusLabel, getFingerprintStatusBadgeClass,
    getQualityStatusLabel, getQualityStatusBadgeClass, formatProbability
  }
}
