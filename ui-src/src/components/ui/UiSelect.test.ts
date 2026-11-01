import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import UiSelect from './UiSelect.vue'

// UiSelect - кастомный дропдаун (legacy .psel): клик мимо/Escape закрывают,
// скролл/ресайз вьюпорта закрывают (якорь уезжает от fixed-списка). НО
// скролл САМОГО списка не закрывает: его scroll-событие на capture-фазе
// долетает до window раньше всего, и без проверки таргета список было
// невозможно прокрутить (жалоба владельца 2026-11-01).

const options = [
  { value: 'tg', label: 'Telegram (домены + подсети IPv4/IPv6)' },
  { value: 'yt', label: 'YouTube' },
]

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('UiSelect', () => {
  it('клик по кнопке открывает список, выбор опции эмитит и закрывает', async () => {
    const w = mount(UiSelect, { props: { modelValue: '', options } })
    expect(w.find('.psel-list').exists()).toBe(false)
    await w.find('.psel-btn').trigger('click')
    await nextTick()
    expect(w.find('.psel-list').exists()).toBe(true)
    await w.find('.psel-item').trigger('click')
    expect(w.emitted('update:modelValue')?.[0]).toEqual(['tg'])
    expect(w.find('.psel-list').exists()).toBe(false)
    w.unmount()
  })

  it('Escape закрывает', async () => {
    const w = mount(UiSelect, { props: { modelValue: '', options } })
    await w.find('.psel-btn').trigger('click')
    await nextTick()
    expect(w.find('.psel-list').exists()).toBe(true)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await w.vm.$nextTick()
    expect(w.find('.psel-list').exists()).toBe(false)
    w.unmount()
  })

  it('скролл вне селекта закрывает (якорь уезжает от fixed-списка)', async () => {
    const w = mount(UiSelect, { props: { modelValue: '', options }, attachTo: document.body })
    await w.find('.psel-btn').trigger('click')
    await nextTick()
    expect(w.find('.psel-list').exists()).toBe(true)
    document.body.dispatchEvent(new Event('scroll'))
    await w.vm.$nextTick()
    expect(w.find('.psel-list').exists()).toBe(false)
    w.unmount()
  })

  it('РЕГРЕСС: скролл самого списка НЕ закрывает - список можно прокрутить', async () => {
    const w = mount(UiSelect, { props: { modelValue: '', options }, attachTo: document.body })
    await w.find('.psel-btn').trigger('click')
    await nextTick()
    const list = w.find('.psel-list')
    expect(list.exists()).toBe(true)
    list.element.dispatchEvent(new Event('scroll'))
    await w.vm.$nextTick()
    expect(list.exists()).toBe(true) // осталось открытым
    // и опция в нём по-прежнему выбирается
    await list.find('.psel-item').trigger('click')
    expect(w.emitted('update:modelValue')?.[0]).toEqual(['tg'])
    w.unmount()
  })
})
