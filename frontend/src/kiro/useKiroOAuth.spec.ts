import { describe, expect, it } from 'vitest'

import { normalizeKiroTokenJSON } from './useKiroOAuth'

describe('normalizeKiroTokenJSON', () => {
  it('unwraps a single-account Kiro Account Manager export and keeps machineId', () => {
    const raw = JSON.stringify([{ accessToken: 'a', refreshToken: 'r', machineId: ' m-1 ' }])
    const result = normalizeKiroTokenJSON(raw)
    expect(result?.machineId).toBe('m-1')
    expect(JSON.parse(result!.tokenJSON)).toEqual({ accessToken: 'a', refreshToken: 'r', machineId: ' m-1 ' })
  })

  it('rejects an export containing multiple accounts', () => {
    expect(normalizeKiroTokenJSON(JSON.stringify([{ accessToken: 'a' }, { accessToken: 'b' }]))).toBeNull()
    expect(normalizeKiroTokenJSON('[]')).toBeNull()
  })

  it('passes a plain token object through without machineId', () => {
    const result = normalizeKiroTokenJSON('{"accessToken":"a","refreshToken":"r"}')
    expect(result?.machineId).toBeUndefined()
    expect(JSON.parse(result!.tokenJSON)).toEqual({ accessToken: 'a', refreshToken: 'r' })
  })

  it('leaves invalid JSON for the backend to report', () => {
    expect(normalizeKiroTokenJSON('not json')).toEqual({ tokenJSON: 'not json' })
  })
})
