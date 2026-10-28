import { describe, expect, it } from 'vitest'
import { formatOllamaResetText, isOllamaCloudAccount, isValidOllamaBaseUrl } from '../ollamaUsage'

describe('Ollama reset hints', () => {
  const zh = (_: string, params: Record<string, string>) => `约${params.time}`
  const en = (_: string, params: Record<string, string>) => `${params.time} (approx.)`
  it('localizes an upstream week hint without converting it to an absolute date', () => {
    expect(formatOllamaResetText('Refills in 1 week', 'zh', zh)).toBe('约1周后')
    expect(formatOllamaResetText('Refills in 2 days.', 'zh', zh)).toBe('约2天后')
    expect(formatOllamaResetText('Refills in 1 week', 'en', en)).toBe('in 1 week (approx.)')
  })
  it('preserves unknown text and does not invent missing data', () => {
    expect(formatOllamaResetText(undefined, 'en', en)).toBeUndefined()
    expect(formatOllamaResetText('Next billing date unknown', 'en', en)).toBe('Next billing date unknown')
  })
})

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
