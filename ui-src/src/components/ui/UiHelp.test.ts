import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import UiHelp from './UiHelp.vue'

// UiHelp - кликабельная справка [?]: поповер с подробным текстом

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('UiHelp', () => {
  it('клик открывает поповер с текстом, повторный - закрывает', async () => {
    const w = mount(UiHelp, { props: { text: 'Канарейка - это проверка доступности.\nВторая строка.' } })
    expect(w.find('.uihelp-pop').exists()).toBe(false)
    await w.find('.uihelp').trigger('click')
    expect(w.find('.uihelp-pop').exists()).toBe(true)
    expect(w.find('.uihelp-pop').text()).toContain('проверка доступности')
    expect(w.find('.uihelp').attributes('aria-expanded')).toBe('true')
    await w.find('.uihelp').trigger('click')
    expect(w.find('.uihelp-pop').exists()).toBe(false)
    w.unmount()
  })

  it('клик мимо закрывает открытый поповер', async () => {
    const w = mount(UiHelp, { props: { text: 'текст' }, attachTo: document.body })
    await w.find('.uihelp').trigger('click')
    expect(w.find('.uihelp-pop').exists()).toBe(true)
    document.body.click()
    await w.vm.$nextTick()
    expect(w.find('.uihelp-pop').exists()).toBe(false)
    w.unmount()
  })

  it('Escape закрывает', async () => {
    const w = mount(UiHelp, { props: { text: 'текст' } })
    await w.find('.uihelp').trigger('click')
    expect(w.find('.uihelp-pop').exists()).toBe(true)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await w.vm.$nextTick()
    expect(w.find('.uihelp-pop').exists()).toBe(false)
    w.unmount()
  })
})
