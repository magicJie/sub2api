import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import ProxiesView from '../ProxiesView.vue'
import ProxyProtocolOptionsFields from '@/components/admin/proxy/ProxyProtocolOptionsFields.vue'
import {
  buildProxyOptions,
  resolveProxyOptions,
  validateProxySecret
} from '@/utils/proxyProtocol'

const { list, create, update, parseLink, batchCreate, getAllWithCount, showError, showSuccess } =
  vi.hoisted(() => ({
    list: vi.fn(),
    create: vi.fn(),
    update: vi.fn(),
    parseLink: vi.fn(),
    batchCreate: vi.fn(),
    getAllWithCount: vi.fn(),
    showError: vi.fn(),
    showSuccess: vi.fn()
  }))
vi.mock('@/api/admin', () => ({
  adminAPI: { proxies: { list, create, update, parseLink, batchCreate, getAllWithCount } }
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } })
}))
vi.mock('@/proxyUpstreamGroups/api', () => ({
  upstreamGroupsAPI: { list: vi.fn().mockResolvedValue([]) },
  upstreamEndpointsAPI: { list: vi.fn().mockResolvedValue([]) }
}))
vi.mock('@/proxyPool/useProxyPools', () => ({
  useProxyPoolOptions: () => ({ poolOptions: [], loadPools: vi.fn() })
}))

const SelectStub = {
  props: ['id', 'modelValue', 'options', 'ariaLabel'],
  emits: ['update:modelValue', 'change'],
  template: `<select :id="id" :aria-label="ariaLabel" :value="modelValue"
    @change="$emit('update:modelValue', $event.target.value); $emit('change', $event.target.value)">
    <option v-for="opt in options" :key="String(opt.value)" :value="opt.value">{{ opt.label }}</option>
  </select>`
}

const mountView = () =>
  shallowMount(ProxiesView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /></div>' },
        DataTable: {
          props: ['data'],
          template: '<div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div>'
        },
        BaseDialog: {
          props: ['show'],
          template: '<div v-if="show"><slot /><slot name="footer" /></div>'
        },
        ProxyProtocolOptionsFields,
        Select: SelectStub
      }
    }
  })
let wrapper: ReturnType<typeof mountView>
beforeEach(() => {
  vi.clearAllMocks()
  list.mockResolvedValue({ items: [{ id: 9, name: 'proxy', protocol: 'http', host: 'proxy.example', port: 8080, username: 'old-user', status: 'active' }], total: 1, pages: 1 })
  getAllWithCount.mockResolvedValue([])
  create.mockResolvedValue({})
  update.mockResolvedValue({})
  parseLink.mockResolvedValue({})
  batchCreate.mockResolvedValue({ created: 1, skipped: 0 })
}
)
afterEach(() => wrapper?.unmount())
async function edit() {
  wrapper = mountView(); await flushPromises()
  await wrapper.findAll('button').find(button => button.text() === 'common.edit')!.trigger('click')
}
async function submit() {
  await wrapper.get('#edit-proxy-form').trigger('submit'); await flushPromises()
  expect(update).toHaveBeenCalledTimes(1)
  return update.mock.calls[0][1]
}

describe('proxy credential updates', () => {
  it('sends an explicit empty username when cleared', async () => {
    await edit()
    const username = wrapper.findAll<HTMLInputElement>('#edit-proxy-form input').find(input => input.element.value === 'old-user')!
    await username.setValue('')
    const payload = await submit()
    expect(payload.username).toBe('')
    expect(payload).not.toHaveProperty('password')
  })

  it('sends an explicit empty password when the user clears the field', async () => {
    await edit()
    await wrapper.get('#edit-proxy-form input[type="password"]').setValue('')
    expect((await submit()).password).toBe('')
  })

  it('omits an untouched password when saving other settings', async () => {
    await edit()
    const payload = await submit()
    expect(payload.username).toBe('old-user')
    expect(payload).not.toHaveProperty('password')
  })

  it('keeps trimming a replacement password', async () => {
    await edit()
    await wrapper.get('#edit-proxy-form input[type="password"]').setValue(' new-password ')
    expect((await submit()).password).toBe('new-password')
  })
})

describe('AnyTLS protocol and Reality spider_x migration behavior', () => {

  it('isolates protocol options when switching between VLESS/Trojan and AnyTLS', async () => {
    wrapper = mountView()
    await flushPromises()
    await wrapper
      .findAll('button')
      .find((b) => b.text().includes('admin.proxies.createProxy'))!
      .trigger('click')

    const protocolSelect = wrapper.get('#create-proxy-form select[aria-label="admin.proxies.protocol"]')
    await protocolSelect.setValue('vless')
    await flushPromises()

    await wrapper.get('#create-opt-flow').setValue('xtls-rprx-vision')
    await wrapper.get('#create-opt-network').setValue('ws')
    await flushPromises()
    await wrapper.get('#create-opt-path').setValue('/ws-path')
    await wrapper.get('#create-opt-tls').setValue('reality')
    await flushPromises()
    await wrapper.get('#create-opt-public_key').setValue('reality-pub-key')

    // Switch to AnyTLS: VLESS/transport/reality options must not leak
    await protocolSelect.setValue('anytls')
    await flushPromises()

    const textInputs = wrapper.findAll('#create-proxy-form input[type="text"]')
    await textInputs[1].setValue('US-AnyTLS')
    await textInputs[2].setValue('us.example.com')
    await wrapper.get('#create-proxy-password').setValue('secret-pass')
    await wrapper.get('#create-opt-skip_cert_verify').setValue('true')

    await wrapper.get('#create-proxy-form').trigger('submit')
    await flushPromises()

    expect(create).toHaveBeenCalledTimes(1)
    const options = create.mock.calls[0][0].options
    expect(options).toEqual({
      skip_cert_verify: 'true'
    })
    expect(options).not.toHaveProperty('flow')
    expect(options).not.toHaveProperty('network')
    expect(options).not.toHaveProperty('path')
    expect(options).not.toHaveProperty('tls')
    expect(options).not.toHaveProperty('public_key')

    const vlessOptions = buildProxyOptions('vless', {
      skip_cert_verify: 'true',
      idle_session_timeout: '60',
      network: 'tcp',
      tls: 'none'
    })
    expect(vlessOptions).not.toHaveProperty('skip_cert_verify')
    expect(vlessOptions).not.toHaveProperty('idle_session_timeout')
  })

  it('retains existing AnyTLS password when untouched on edit, and persists explicit cert verification toggle from true to false', async () => {
    list.mockResolvedValue({
      items: [
        {
          id: 42,
          name: 'HK-AnyTLS',
          protocol: 'anytls',
          host: 'hk.example.com',
          port: 443,
          username: null,
          status: 'active',
          options: {
            sni: 'hk.example.com',
            skip_cert_verify: 'true',
            idle_session_timeout: '45'
          }
        }
      ],
      total: 1,
      pages: 1
    })

    await edit()

    const skipSelect = wrapper.get<HTMLSelectElement>('#edit-opt-skip_cert_verify')
    expect(skipSelect.element.value).toBe('true')

    // Explicitly switch certificate verification back to default ('false') without touching password
    await skipSelect.setValue('false')
    const payload = await submit()

    expect(payload).not.toHaveProperty('password')
    expect(payload.username).toBe('')
    expect(payload.options).toEqual({
      sni: 'hk.example.com',
      skip_cert_verify: 'false',
      idle_session_timeout: '45'
    })
  })

  it('rejects clearing password on an AnyTLS proxy during edit or switching from plain HTTP without password', async () => {
    list.mockResolvedValue({
      items: [
        {
          id: 43,
          name: 'HK-AnyTLS',
          protocol: 'anytls',
          host: 'hk.example.com',
          port: 443,
          password: 'old-anytls-password',
          status: 'active',
          options: { skip_cert_verify: 'false' }
        }
      ],
      total: 1,
      pages: 1
    })

    await edit()
    await wrapper.get('#edit-proxy-password').setValue('')
    await wrapper.get('#edit-proxy-form').trigger('submit')
    await flushPromises()

    expect(update).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.proxies.secretRequired')

    expect(
      validateProxySecret('anytls', 'valid-pass', {
        skip_cert_verify: 'false',
        idle_session_timeout: '10s'
      })
    ).toBe('admin.proxies.anytlsSessionIntInvalid')
  })


  it('does not render spider_x input for Reality, rejects legacy non-empty spider_x until explicitly cleared', async () => {
    expect(
      resolveProxyOptions('vless', {
        tls: 'reality',
        public_key: 'pk',
        spider_x: '/legacy'
      }).spider_x
    ).toBe('/legacy')
    expect(
      buildProxyOptions('vless', {
        tls: 'reality',
        public_key: 'pk',
        spider_x: '/legacy'
      }).spider_x
    ).toBe('/legacy')
    expect(
      validateProxySecret(
        'vless',
        '11111111-1111-1111-1111-111111111111',
        buildProxyOptions('vless', {
          tls: 'reality',
          public_key: 'pk',
          spider_x: '/legacy'
        })
      )
    ).toBe('admin.proxies.realitySpiderXUnsupported')

    list.mockResolvedValue({
      items: [
        {
          id: 77,
          name: 'Legacy-Reality',
          protocol: 'vless',
          host: 'vless.example.com',
          port: 443,
          status: 'active',
          options: {
            network: 'tcp',
            tls: 'reality',
            sni: 'vless.example.com',
            public_key: 'reality-pub-key',
            short_id: 'abcd',
            spider_x: '/legacy-spider'
          }
        }
      ],
      total: 1,
      pages: 1
    })

    await edit()

    // spider_x text input does not exist, but legacy clear button is shown
    expect(wrapper.find('input#edit-opt-spider_x').exists()).toBe(false)
    const clearBtn = wrapper.find('#edit-opt-clear-spider-x')
    expect(clearBtn.exists()).toBe(true)

    // Submitting without clearing spider_x is rejected
    await wrapper.get('#edit-proxy-form').trigger('submit')
    await flushPromises()
    expect(update).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.proxies.realitySpiderXUnsupported')

    // Explicitly clearing spider_x removes it and allows saving
    await clearBtn.trigger('click')
    await flushPromises()
    expect(wrapper.find('#edit-opt-clear-spider-x').exists()).toBe(false)

    const payload = await submit()
    expect(payload.options).not.toHaveProperty('spider_x')
    expect(payload.options).toMatchObject({
      network: 'tcp',
      tls: 'reality',
      sni: 'vless.example.com',
      public_key: 'reality-pub-key',
      short_id: 'abcd'
    })
  })
})
