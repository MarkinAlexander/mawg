import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import UiHelp from './UiHelp.vue'

// UiHelp - кликабельная справка [?]: поповер с подробным текстом.
// Позиционирование fixed с клампом в вьюпорт (67-док): не вылезает за
// края, слова не бьются; скролл/ресайз закрывают.

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('UiHelp', () => {
  it('клик открывает поповер с текстом, повторный - закрывает', async () => {
    const w = mount(UiHelp, { props: { text: 'Канарейка - это проверка доступности.\nВторая строка.' } })
    expect(w.find('.uihelp-pop').exists()).toBe(false)
    await w.find('.uihelp').trigger('click')
    await nextTick()
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
    await nextTick()
    expect(w.find('.uihelp-pop').exists()).toBe(true)
    document.body.click()
    await w.vm.$nextTick()
    expect(w.find('.uihelp-pop').exists()).toBe(false)
    w.unmount()
  })

  it('Escape закрывает', async () => {
    const w = mount(UiHelp, { props: { text: 'текст' } })
    await w.find('.uihelp').trigger('click')
    await nextTick()
    expect(w.find('.uihelp-pop').exists()).toBe(true)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await w.vm.$nextTick()
    expect(w.find('.uihelp-pop').exists()).toBe(false)
    w.unmount()
  })

  it('позиционирование: position fixed, ширина в границах вьюпорта, видимость после расчёта', async () => {
    const w = mount(UiHelp, { props: { text: 'текст подсказки' }, attachTo: document.body })
    await w.find('.uihelp').trigger('click')
    await vi.waitFor(() => {
      const style = w.find('.uihelp-pop').attributes('style') || ''
      expect(style).toContain('position: fixed')
      expect(style).toContain('width: 290px') // min(290, innerWidth - 16)
    })
    const style = w.find('.uihelp-pop').attributes('style') || ''
    expect(style).not.toContain('visibility: hidden') // расчёт завершён
    w.unmount()
  })

  it('скролл и ресайз закрывают поповер (не «отвисает» от своего [?])', async () => {
    const w = mount(UiHelp, { props: { text: 'текст' }, attachTo: document.body })
    await w.find('.uihelp').trigger('click')
    await nextTick()
    expect(w.find('.uihelp-pop').exists()).toBe(true)
    window.dispatchEvent(new Event('scroll'))
    await w.vm.$nextTick()
    expect(w.find('.uihelp-pop').exists()).toBe(false)
    await w.find('.uihelp').trigger('click')
    await nextTick()
    expect(w.find('.uihelp-pop').exists()).toBe(true)
    window.dispatchEvent(new Event('resize'))
    await w.vm.$nextTick()
    expect(w.find('.uihelp-pop').exists()).toBe(false)
    w.unmount()
  })
})
