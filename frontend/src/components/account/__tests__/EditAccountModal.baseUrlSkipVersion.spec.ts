import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'

const { updateAccountMock, checkMixedChannelRiskMock, authIsSimpleMode } = vi.hoisted(() => ({
  updateAccountMock: vi.fn(),
  checkMixedChannelRiskMock: vi.fn(),
  authIsSimpleMode: { value: true }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn()
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    get isSimpleMode() {
      return authIsSimpleMode.value
    }
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      update: updateAccountMock,
      checkMixedChannelRisk: checkMixedChannelRiskMock
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({})
    },
    tlsFingerprintProfiles: {
      list: vi.fn().mockResolvedValue([])
    }
  }
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn()
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

import EditAccountModal from '../EditAccountModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: {
    show: {
      type: Boolean,
      default: false
    }
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

function buildAccount(
  overrides: Record<string, unknown> = {},
  credentials: Record<string, unknown> = {}
) {
  return {
    id: 29,
    name: 'upstream-gpt-image',
    notes: '',
    platform: 'openai',
    type: 'apikey',
    credentials: {
      base_url: 'https://image.aigw.store/api-proxy',
      ...credentials
    },
    credentials_status: { has_api_key: true },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false,
    ...overrides
  } as any
}

function mountModal(account: any) {
  return mount(EditAccountModal, {
    props: {
      show: true,
      account,
      proxies: [],
      groups: []
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Select: true,
        Icon: true,
        ProxySelector: true,
        GroupSelector: true,
        ModelWhitelistSelector: true
      }
    }
  })
}

describe('EditAccountModal base_url_skip_version', () => {
  beforeEach(() => {
    authIsSimpleMode.value = true
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
  })

  it('persists the flag when ticked on an openai apikey account', async () => {
    const account = buildAccount()
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const checkbox = wrapper.get('[data-testid="base-url-skip-version"]')
    expect((checkbox.element as HTMLInputElement).checked).toBe(false)

    await checkbox.setValue(true)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await vi.waitFor(() => expect(updateAccountMock).toHaveBeenCalledTimes(1))

    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.base_url_skip_version).toBe(true)
  })

  it('echoes a stored flag and removes it when unticked', async () => {
    const account = buildAccount({}, { base_url_skip_version: true })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const checkbox = wrapper.get('[data-testid="base-url-skip-version"]')
    expect((checkbox.element as HTMLInputElement).checked).toBe(true)

    await checkbox.setValue(false)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await vi.waitFor(() => expect(updateAccountMock).toHaveBeenCalledTimes(1))

    const credentials = updateAccountMock.mock.calls[0]?.[1]?.credentials
    expect(credentials).toBeDefined()
    expect('base_url_skip_version' in (credentials as Record<string, unknown>)).toBe(false)
  })

  it('leaves credentials untouched when the flag is never ticked', async () => {
    const account = buildAccount()
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await vi.waitFor(() => expect(updateAccountMock).toHaveBeenCalledTimes(1))

    const credentials = updateAccountMock.mock.calls[0]?.[1]?.credentials
    expect('base_url_skip_version' in (credentials as Record<string, unknown>)).toBe(false)
  })

  it('is hidden for platforms outside the OpenAI protocol family', async () => {
    const account = buildAccount({ platform: 'anthropic' })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    expect(wrapper.find('[data-testid="base-url-skip-version"]').exists()).toBe(false)
  })
})
