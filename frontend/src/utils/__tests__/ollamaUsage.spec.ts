import { describe, expect, it } from 'vitest'
import { formatOllamaSnapshotError, isOllamaCloudAccount, isValidOllamaBaseUrl } from '../ollamaUsage'
describe('isOllamaCloudAccount', () => {
  it('matches only ollama apikey accounts', () => {
    expect(isOllamaCloudAccount({ platform: 'ollama', type: 'apikey' })).toBe(true)
    expect(isOllamaCloudAccount({ platform: 'ollama', type: 'oauth' })).toBe(false)
    expect(isOllamaCloudAccount({ platform: 'openai', type: 'apikey' })).toBe(false)
    expect(isOllamaCloudAccount(null)).toBe(false)
    expect(isOllamaCloudAccount(undefined)).toBe(false)
  })
})

describe('isValidOllamaBaseUrl', () => {
  it.each(['', ' https://ollama.com/v1 ', 'https://ollama.com', 'https://ollama.com/', 'https://OLLAMA.COM:443/v1/'])(
    'accepts valid official endpoint %j',
    (url) => {
      expect(isValidOllamaBaseUrl(url)).toBe(true)
    }
  )

  it.each([
    'https://www.ollama.com/v1',
    'https://ollama.com.evil.test',
    'http://ollama.com',
    'https://ollama.com:444',
    'https://user@ollama.com',
    'https://ollama.com/v2',
    'https://ollama.com?next=evil',
    'https://ollama.com#x'
  ])('rejects invalid or non-official endpoint %s', (url) => {
    expect(isValidOllamaBaseUrl(url)).toBe(false)
  })
})

describe('formatOllamaSnapshotError', () => {
  const t = (key: string) => key

  it('returns empty string for healthy snapshots', () => {
    expect(formatOllamaSnapshotError({ status: 'ok' }, t)).toBe('')
  })

  it('distinguishes HTTP 403 suspended accounts from HTTP 401 invalid API keys and invalid JSON', () => {
    expect(formatOllamaSnapshotError({
      status: 'failed',
      last_error: 'http_error',
      http_status: 403
    }, t)).toBe('admin.accounts.ollamaCloud.errors.account_suspended (HTTP 403)')

    expect(formatOllamaSnapshotError({
      status: 'unauthorized',
      last_error: 'unauthorized',
      http_status: 401
    }, t)).toBe('admin.accounts.ollamaCloud.errors.unauthorized (HTTP 401)')

    expect(formatOllamaSnapshotError({
      status: 'failed',
      last_error: 'invalid_json'
    }, t)).toBe('admin.accounts.ollamaCloud.errors.invalid_json')
  })
})
