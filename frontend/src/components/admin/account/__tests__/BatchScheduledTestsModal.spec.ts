import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import BatchScheduledTestsModal from '../BatchScheduledTestsModal.vue'
import type { BatchCreateScheduledTestPlansResponse } from '@/types'

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key) })
}))

enableAutoUnmount(afterEach)

const { batchCreate, getAvailableModels, showError, showSuccess, showWarning } = vi.hoisted(() => ({
  batchCreate: vi.fn(),
  getAvailableModels: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    scheduledTests: { batchCreate },
    accounts: { getAvailableModels }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess, showWarning })
}))

function mountModal(accountIds = [1, 2, 999, 2]) {
  return mount(BatchScheduledTestsModal, {
    props: {
      show: true,
      accountIds,
      selectedPlatforms: ['anthropic']
    },
    global: {
      stubs: {
        BaseDialog: {
          props: ['show'],
          template: '<div v-if="show"><slot /><slot name="footer" /></div>'
        },
        HelpTooltip: {
          template: '<div><slot name="trigger" /><slot /></div>'
        }
      }
    }
  })
}

describe('BatchScheduledTestsModal', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    getAvailableModels.mockResolvedValue([{ id: 'claude-sonnet-4-5', display_name: 'Claude Sonnet 4.5' }])
  })

  it('keeps a stable target selection and retries only failed accounts after partial failure', async () => {
    const partialResponse: BatchCreateScheduledTestPlansResponse = {
      created: 1,
      skipped: 1,
      failed: 1,
      results: [
        { account_id: 1, status: 'created' },
        { account_id: 2, status: 'skipped' },
        { account_id: 999, status: 'failed', error: 'account not found' }
      ]
    }
    const retryResponse: BatchCreateScheduledTestPlansResponse = {
      created: 1,
      skipped: 0,
      failed: 0,
      results: [{ account_id: 999, status: 'created' }]
    }
    batchCreate.mockResolvedValueOnce(partialResponse).mockResolvedValueOnce(retryResponse)

    const wrapper = mountModal([1, 2, 999, 2])
    await flushPromises()

    const modelInput = wrapper.find('[data-testid="batch-schedule-model-input"] input')
    await modelInput.setValue('claude-sonnet-4-5')

    const submitBtn = wrapper.find('[data-testid="batch-schedule-submit-btn"]')
    await submitBtn.trigger('click')
    await flushPromises()

    expect(batchCreate).toHaveBeenCalledTimes(1)
    expect(batchCreate).toHaveBeenNthCalledWith(
      1,
      expect.objectContaining({ account_ids: [1, 2, 999] })
    )

    expect(wrapper.find('[data-testid="batch-schedule-created-count"]').text()).toBe('1')
    expect(wrapper.find('[data-testid="batch-schedule-skipped-count"]').text()).toBe('1')
    expect(wrapper.find('[data-testid="batch-schedule-failed-count"]').text()).toBe('1')
    const failedItems = wrapper.findAll('[data-testid="batch-schedule-failed-item"]')
    expect(failedItems).toHaveLength(1)
    expect(failedItems[0].text()).toContain('999')
    expect(failedItems[0].text()).toContain('account not found')

    // Even if parent accountIds prop updates while partial failure is active, retry target stays locked to failed IDs
    await wrapper.setProps({ accountIds: [1, 2, 3, 4, 999] })
    const retryBtn = wrapper.find('[data-testid="batch-schedule-retry-failed-btn"]')
    expect(retryBtn.exists()).toBe(true)

    await retryBtn.trigger('click')
    await flushPromises()

    expect(batchCreate).toHaveBeenCalledTimes(2)
    expect(batchCreate).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({
        account_ids: [999],
        model_id: 'claude-sonnet-4-5'
      })
    )
    expect(wrapper.findAll('[data-testid="batch-schedule-failed-item"]')).toHaveLength(0)
    expect(wrapper.find('[data-testid="batch-schedule-retry-failed-btn"]').exists()).toBe(false)
  })

  it('prevents duplicate submissions and blocks closing while batch request is in flight', async () => {
    const pending = Promise.withResolvers<BatchCreateScheduledTestPlansResponse>()
    batchCreate.mockReturnValueOnce(pending.promise)

    const wrapper = mountModal([10, 20])
    await flushPromises()

    await wrapper.find('[data-testid="batch-schedule-model-input"] input').setValue('gpt-4o')
    const submitBtn = wrapper.find('[data-testid="batch-schedule-submit-btn"]')
    await submitBtn.trigger('click')
    await submitBtn.trigger('click')
    await wrapper.find('[data-testid="batch-schedule-close-btn"]').trigger('click')

    expect(batchCreate).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('close')).toBeUndefined()

    pending.resolve({
      created: 2,
      skipped: 0,
      failed: 0,
      results: [
        { account_id: 10, status: 'created' },
        { account_id: 20, status: 'created' }
      ]
    })
    await flushPromises()

    await wrapper.find('[data-testid="batch-schedule-close-btn"]').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
  })
})
