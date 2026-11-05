import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'

vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))
enableAutoUnmount(afterEach)

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

import ScheduledTestsPanel from '../ScheduledTestsPanel.vue'
import type { ScheduledTestResult } from '@/types'
const { listByAccount, listResults, runNow, create, update, showError, showSuccess } = vi.hoisted(() => ({
  listByAccount: vi.fn(),
  listResults: vi.fn(),
  runNow: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))
vi.mock('@/api/admin', () => ({
  adminAPI: { scheduledTests: { listByAccount, listResults, runNow, create, update } }
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess }) }))

async function openPanel() {
  const wrapper = shallowMount(ScheduledTestsPanel, {
    props: { show: false, accountId: 1, modelOptions: [] },
    global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } },
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}
const result = (id: number) => ({ id, latency_ms: id, status: 'success', started_at: '2026-10-03T00:00:00Z' })

describe('scheduled test result selection', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    listByAccount.mockResolvedValue([1, 2].map(id => ({ id, model_id: `model-${id}`, enabled: true })))
  })

  it.each(['resolve', 'reject'] as const)('ignores an old plan request that later %ss', async outcome => {
    const old = deferred<ReturnType<typeof result>[]>()
    listResults.mockReturnValueOnce(old.promise).mockResolvedValueOnce([result(22)])
    const wrapper = await openPanel()
    const headers = wrapper.findAll('.cursor-pointer')
    await headers[0].trigger('click')
    await headers[1].trigger('click')
    await flushPromises()
    const current = wrapper.html()
    if (outcome === 'resolve') old.resolve([result(11)])
    else old.reject(new Error('obsolete error'))
    await flushPromises()
    expect(wrapper.html()).toBe(current)
    expect(showError).not.toHaveBeenCalled()
  })

  it('keeps loading the current plan when the previous request completes', async () => {
    const old = deferred<ReturnType<typeof result>[]>()
    const current = deferred<ReturnType<typeof result>[]>()
    listResults.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const wrapper = await openPanel()
    await wrapper.findAll('.cursor-pointer')[0].trigger('click')
    await wrapper.findAll('.cursor-pointer')[1].trigger('click')
    old.resolve([result(11)])
    await flushPromises()
    expect(wrapper.text()).toContain('common.loading')
    current.resolve([result(22)])
    await flushPromises()
    expect(wrapper.text()).not.toContain('common.loading')
  })

  it('invalidates a request when the same plan is collapsed and reopened', async () => {
    const old = deferred<ReturnType<typeof result>[]>()
    listResults.mockReturnValueOnce(old.promise).mockResolvedValueOnce([result(22)])
    const wrapper = await openPanel()
    const header = wrapper.findAll('.cursor-pointer')[0]
    await header.trigger('click')
    await header.trigger('click')
    await header.trigger('click')
    await flushPromises()
    const current = wrapper.html()
    old.resolve([result(11)])
    await flushPromises()
    expect(wrapper.html()).toBe(current)
  })

  it('runs a plan immediately, expands results, and renders diagnostic warning summary', async () => {
    const diagnosticResult = {
      id: 99,
      plan_id: 1,
      status: 'warning',
      response_text: '',
      error_message: 'fingerprint mismatch; ability below threshold',
      latency_ms: 420,
      started_at: '2026-11-05T10:00:00Z',
      finished_at: '2026-11-05T10:00:01Z',
      created_at: '2026-11-05T10:00:01Z',
      diagnostics: {
        version: 'v1',
        test_type: 'comprehensive',
        model: 'claude-sonnet-4-5',
        expected_model: 'claude-opus-4-6',
        fingerprint: {
          status: 'mismatch',
          prediction: 'claude-sonnet-4-5',
          probability: 0.912,
          used_outputs: 3,
          candidates: [
            { model: 'claude-sonnet-4-5', probability: 0.912, family: 'claude' },
            { model: 'claude-opus-4-6', probability: 0.064, family: 'claude' }
          ]
        },
        quality: {
          status: 'degraded',
          score: 60,
          threshold: 80,
          passed: 3,
          total: 5,
          checks: [
            { id: 'math_exact', passed: true, expected: '42', actual: '42' },
            { id: 'logic_order', passed: false, expected: 'B>A>C', actual: 'A>B>C' }
          ]
        }
      }
    }
    runNow.mockResolvedValueOnce(diagnosticResult)
    listResults.mockResolvedValueOnce([diagnosticResult])

    const wrapper = await openPanel()
    const runButtons = wrapper.findAll('[data-testid="run-plan-btn"]')
    expect(runButtons.length).toBe(2)

    await runButtons[0].trigger('click')
    await flushPromises()

    expect(runNow).toHaveBeenCalledWith(1)
    expect(listResults).toHaveBeenCalledWith(1, 20)
    expect(wrapper.text()).toContain('admin.scheduledTests.warning')
    expect(wrapper.text()).toContain('admin.scheduledTests.fingerprintMismatch')
    expect(wrapper.text()).toContain('claude-sonnet-4-5')
    expect(wrapper.text()).toContain('91.2%')
    expect(wrapper.text()).toContain('admin.scheduledTests.qualityDegraded')
    expect(wrapper.text()).toContain('60/100')
    expect(wrapper.text()).toContain('logic_order')
  })

  it('ignores stale runNow completion when the panel closes before it finishes', async () => {
    const pendingRun = deferred<Partial<ScheduledTestResult>>()
    runNow.mockReturnValueOnce(pendingRun.promise)

    const wrapper = await openPanel()
    await wrapper.findAll('[data-testid="run-plan-btn"]')[0].trigger('click')
    await wrapper.setProps({ show: false })
    pendingRun.reject(new Error('late failure after close'))
    await flushPromises()

    expect(showError).not.toHaveBeenCalled()
    expect(listResults).not.toHaveBeenCalled()
  })

  it('keeps a reopened plan busy when the previous panel run finishes', async () => {
    const previous = deferred<Partial<ScheduledTestResult>>()
    const current = deferred<Partial<ScheduledTestResult>>()
    runNow.mockReturnValueOnce(previous.promise).mockReturnValueOnce(current.promise)
    listResults.mockResolvedValue([])
    const wrapper = await openPanel()
    await wrapper.findAll('[data-testid="run-plan-btn"]')[0].trigger('click')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.findAll('[data-testid="run-plan-btn"]')[0].trigger('click')
    previous.reject(new Error('previous panel finished'))
    await flushPromises()
    expect(wrapper.findAll('[data-testid="run-plan-btn"]')[0].attributes('disabled')).toBeDefined()
    current.resolve({ ...result(33), finished_at: '2026-11-05T10:00:01Z' })
    await flushPromises()
    expect(wrapper.findAll('[data-testid="run-plan-btn"]')[0].attributes('disabled')).toBeUndefined()
    expect(showError).not.toHaveBeenCalled()
  })
})
