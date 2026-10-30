import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

vi.mock('@/constants/platforms', () => ({
  CONCRETE_PLATFORM_OPTIONS: [
    { value: 'anthropic', label: 'Anthropic' },
    { value: 'openai', label: 'OpenAI' },
    { value: 'kiro', label: 'Kiro' }
  ]
}))

const BaseDialogStub = {
  props: ['show', 'title'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
}
const SelectStub = { props: ['modelValue', 'options'], template: '<div />' }

const loadModules = async () => {
  vi.resetModules()
  const store = await import('../accountEnabledPlatforms')
  const Modal = (await import('../AccountPlatformSettingsModal.vue')).default
  const Filters = (await import('../AccountTableFilters.vue')).default
  return { store, Modal, Filters }
}

const platformOptions = (wrapper: ReturnType<typeof mount>) =>
  (wrapper.findAllComponents(SelectStub)[0].props('options') as { value: string }[]).map(o => o.value)

describe('配置账号启用', () => {
  beforeEach(() => localStorage.clear())

  it('未配置时全部平台都可筛选', async () => {
    const { Filters } = await loadModules()
    const wrapper = mount(Filters, {
      props: { searchQuery: '', filters: { platform: '' } },
      global: { stubs: { Select: SelectStub, SearchInput: true } }
    })
    expect(platformOptions(wrapper)).toEqual(['', 'anthropic', 'openai', 'kiro'])
  })

  it('保存后平台筛选只列启用的平台，并写入本地存储', async () => {
    const { store, Modal, Filters } = await loadModules()
    const modal = mount(Modal, { props: { show: true }, global: { stubs: { BaseDialog: BaseDialogStub } } })
    await modal.find('[data-test="platform-anthropic"]').setValue(false)
    await modal.find('[data-test="save"]').trigger('click')
    expect(modal.emitted('saved')).toHaveLength(1)
    expect(JSON.parse(localStorage.getItem(store.ACCOUNT_ENABLED_PLATFORMS_STORAGE_KEY)!)).toEqual(['openai', 'kiro'])

    const filters = mount(Filters, {
      props: { searchQuery: '', filters: { platform: '' } },
      global: { stubs: { Select: SelectStub, SearchInput: true } }
    })
    expect(platformOptions(filters)).toEqual(['', 'openai', 'kiro'])
  })

  it('全选保存等同未配置，清空时不能保存', async () => {
    const { store, Modal } = await loadModules()
    store.saveAccountEnabledPlatforms(['kiro'])
    const modal = mount(Modal, { props: { show: true }, global: { stubs: { BaseDialog: BaseDialogStub } } })
    await modal.find('[data-test="clear"]').trigger('click')
    expect(modal.find('[data-test="save"]').attributes('disabled')).toBeDefined()
    await modal.find('[data-test="select-all"]').trigger('click')
    await modal.find('[data-test="save"]').trigger('click')
    expect(store.accountEnabledPlatforms.value).toBeNull()
    expect(localStorage.getItem(store.ACCOUNT_ENABLED_PLATFORMS_STORAGE_KEY)).toBeNull()
  })
})
