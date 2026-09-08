import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import GroupSelector from '../GroupSelector.vue'
import type { AdminGroup, Group, GroupPlatform } from '@/types'

const authState = { isSimpleMode: false }

vi.mock('@/stores', () => ({ useAuthStore: () => authState }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key,
    }),
  }
})

// simple 模式是全局开关，测试间必须复位，否则后跑的用例会继承前一个的模式。
beforeEach(() => {
  authState.isSimpleMode = false
})

const makeGroup = (id: number, platform: GroupPlatform, name = `g${id}`): AdminGroup =>
  ({
    id,
    name,
    description: null,
    platform,
    rate_multiplier: 1,
    is_exclusive: false,
    status: 'active',
    subscription_type: 'shared',
    account_count: 0,
  }) as unknown as AdminGroup

const mountSelector = (props: Record<string, unknown>) =>
  mount(GroupSelector, {
    props,
    global: { stubs: { GroupBadge: true, Icon: true } },
  })

describe('GroupSelector hidden selections', () => {
  it('renders selected groups that the platform filter excludes', () => {
    const wrapper = mountSelector({
      modelValue: [1, 2, 3],
      groups: [makeGroup(1, 'openai'), makeGroup(2, 'anthropic'), makeGroup(3, 'gemini')],
      platform: 'openai' as GroupPlatform,
    })

    // 只有 1 个分组进入主列表，另外 2 个必须出现在"不可见"区域，否则勾选数与计数对不上
    expect(wrapper.findAll('[data-hidden-group-id]')).toHaveLength(2)
    expect(wrapper.html()).toContain('common.groupSelector.hiddenBadge:{"count":2}')
  })

  it('resolves disabled groups from fallbackGroups and can unbind them', async () => {
    const disabled = { ...makeGroup(9, 'openai', 'disabled-group'), status: 'inactive' } as Group
    const wrapper = mountSelector({
      modelValue: [1, 9],
      groups: [makeGroup(1, 'openai')],
      platform: 'openai' as GroupPlatform,
      fallbackGroups: [disabled],
    })

    const hidden = wrapper.findAll('[data-hidden-group-id]')
    expect(hidden).toHaveLength(1)
    expect(wrapper.html()).toContain('common.groupSelector.reasonInactive')

    await hidden[0].setValue(false)
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([[1]])
  })

  it('labels selected ids with no metadata as unknown', () => {
    const wrapper = mountSelector({
      modelValue: [42],
      groups: [makeGroup(1, 'openai')],
      platform: 'openai' as GroupPlatform,
    })

    expect(wrapper.html()).toContain('common.groupSelector.reasonUnknown')
    expect(wrapper.html()).toContain('common.groupSelector.unknownGroupName:{"id":42}')
  })

  it('counts duplicate ids once and never emits duplicates', async () => {
    const wrapper = mountSelector({
      modelValue: [1, 1],
      groups: [makeGroup(1, 'openai'), makeGroup(2, 'openai')],
      platform: 'openai' as GroupPlatform,
    })

    expect(wrapper.html()).toContain('common.selectedCount:{"count":1}')
    expect(wrapper.findAll('[data-hidden-group-id]')).toHaveLength(0)

    await wrapper.findAll('input[type="checkbox"]')[1].setValue(true)
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([[1, 2]])
  })

  it('keeps search purely visual and does not flag search-hidden selections', async () => {
    const groups = Array.from({ length: 6 }, (_, i) => makeGroup(i + 1, 'openai', `group-${i + 1}`))
    const wrapper = mountSelector({ modelValue: [1, 2], groups, platform: 'openai' as GroupPlatform })

    await wrapper.find('input[type="text"]').setValue('group-6')
    expect(wrapper.findAll('[data-hidden-group-id]')).toHaveLength(0)
  })
})

describe('GroupSelector simple-mode binding policy', () => {
  const simpleModeGroups = [
    { id: 1, name: 'Basic', platform: 'anthropic', status: 'active' },
    { id: 2, name: 'Composite', platform: 'composite', status: 'active' }
  ] as any

  const mountSimpleModeSelector = (modelValue: number[] = []) => mount(GroupSelector, {
    props: { modelValue, groups: simpleModeGroups },
    global: { stubs: { GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' }, Icon: true } }
  })

  it('hides composite groups in simple mode and preserves basic groups', () => {
    authState.isSimpleMode = true
    const wrapper = mountSimpleModeSelector()
    expect(wrapper.text()).toContain('Basic')
    expect(wrapper.text()).not.toContain('Composite')
  })

  it('keeps composite groups available in advanced mode', () => {
    const wrapper = mountSimpleModeSelector()
    expect(wrapper.text()).toContain('Composite')
  })

  it('cleans hidden historical composite IDs while preserving visible selections', () => {
    authState.isSimpleMode = true
    const wrapper = mountSimpleModeSelector([1, 2])
    expect(wrapper.emitted('update:modelValue')).toEqual([[[1]]])
  })
})
