export const DEFAULT_FINGERPRINT_CRON = '0 */6 * * *'

export const PLATFORM_DETECTION_DEFAULTS: Record<string, { model_id: string; expected_model: string }> = {
  anthropic: { model_id: 'claude-opus-5-5', expected_model: 'claude-opus-5-5' },
  openai: { model_id: 'gpt-6.1-sol', expected_model: 'gpt-6.1-sol' }
}

export function resolveDetectionDefaults(platforms: readonly string[]): { model_id: string; expected_model: string } {
  const unique = [...new Set(platforms)]
  const hit = unique.length === 1 ? PLATFORM_DETECTION_DEFAULTS[unique[0]] : undefined
  return hit ? { ...hit } : { model_id: '', expected_model: '' }
}
