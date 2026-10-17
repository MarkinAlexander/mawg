import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import UiBadge from './UiBadge.vue'
import UiBanner from './UiBanner.vue'
import UiButton from './UiButton.vue'
import UiHint from './UiHint.vue'
import UiIcon from './UiIcon.vue'

describe('UiHint', () => {
  it('рендерит текст подсказки', () => {
    const w = mount(UiHint, { props: { text: 'серый hint' } })
    expect(w.text()).toBe('серый hint')
  })
})

describe('UiBadge', () => {
  it('вариант даёт класс, tooltip идёт в title', () => {
    const w = mount(UiBadge, { props: { text: 'работает', variant: 'ok', tooltip: 'пул отвечает' } })
    expect(w.classes()).toContain('v-ok')
    expect(w.attributes('title')).toBe('пул отвечает')
  })

  it('без варианта - нейтральный (muted)', () => {
    const w = mount(UiBadge, { props: { text: 'x' } })
    expect(w.classes()).toContain('v-muted')
  })
})

describe('UiBanner', () => {
  it('kind даёт класс, link рендерится с rel=noopener', () => {
    const w = mount(UiBanner, {
      props: { kind: 'warn', text: 'нужен форк rev3+', link: { href: 'https://example.invalid', label: 'MR !222' } },
    })
    expect(w.classes()).toContain('k-warn')
    expect(w.text()).toContain('нужен форк rev3+')
    const a = w.get('a')
    expect(a.attributes('href')).toBe('https://example.invalid')
    expect(a.attributes('rel')).toBe('noopener')
  })
})

describe('UiButton', () => {
  it('вариант даёт класс, tooltip в title, клик эмитится', async () => {
    const w = mount(UiButton, { props: { variant: 'primary', tooltip: 'сохранить всё' } })
    expect(w.classes()).toContain('v-primary')
    expect(w.attributes('title')).toBe('сохранить всё')
    await w.trigger('click')
    expect(w.emitted('click')).toHaveLength(1)
  })

  it('danger красный по умолчанию (не только hover - кейс телефона)', () => {
    const w = mount(UiButton, { props: { variant: 'danger' } })
    expect(w.classes()).toContain('v-danger')
  })

  it('busy: disabled + класс busy (спиннер), клик не проходит', async () => {
    const w = mount(UiButton, { props: { busy: true } })
    expect(w.classes()).toContain('busy')
    expect(w.attributes('disabled')).toBeDefined()
    await w.trigger('click')
    expect(w.emitted('click')).toBeUndefined()
  })

  it('disabled блокирует клик', async () => {
    const w = mount(UiButton, { props: { disabled: true } })
    expect(w.attributes('disabled')).toBeDefined()
    await w.trigger('click')
    expect(w.emitted('click')).toBeUndefined()
  })
})

describe('UiIcon', () => {
  it('вставляет svg по имени, title в title', () => {
    const w = mount(UiIcon, { props: { name: 'trash', title: 'удалить' } })
    expect(w.find('svg').exists()).toBe(true)
    expect(w.attributes('title')).toBe('удалить')
  })
})
