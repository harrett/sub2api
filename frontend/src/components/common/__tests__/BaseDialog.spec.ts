import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import BaseDialog from '../BaseDialog.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

describe('BaseDialog', () => {
  afterEach(() => {
    document.body.innerHTML = ''
    document.body.classList.remove('modal-open')
  })

  it('resets body scroll position when reopened', async () => {
    const wrapper = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: false, title: 'Details' },
      slots: { default: '<div style="height: 2000px">content</div>' },
      global: { stubs: { Icon: true } }
    })

    await wrapper.setProps({ show: true })
    await nextTick()
    const body = document.body.querySelector<HTMLElement>('.modal-body')
    expect(body).not.toBeNull()
    body!.scrollTop = 480

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await nextTick()

    expect(document.body.querySelector<HTMLElement>('.modal-body')?.scrollTop).toBe(0)
    wrapper.unmount()
  })

  it('keeps scroll lock and only closes the top dialog when nested', async () => {
    const parent = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: true, title: 'Parent' },
      global: { stubs: { Icon: true } }
    })
    const child = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: true, title: 'Child' },
      global: { stubs: { Icon: true } }
    })
    await nextTick()

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(child.emitted('close')).toHaveLength(1)
    expect(parent.emitted('close')).toBeUndefined()

    // Closing the child must not release the scroll lock held by the parent.
    await child.setProps({ show: false })
    expect(document.body.classList.contains('modal-open')).toBe(true)

    await parent.setProps({ show: false })
    expect(document.body.classList.contains('modal-open')).toBe(false)

    child.unmount()
    parent.unmount()
  })
})
