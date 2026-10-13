import { describe, expect, it } from 'vitest'
import { h } from 'vue'
import { mount } from '@vue/test-utils'
import UiCheckbox from './UiCheckbox.vue'
import UiField from './UiField.vue'
import UiInput from './UiInput.vue'
import UiSelect, { type UiSelectOption } from './UiSelect.vue'
import UiSwitch from './UiSwitch.vue'
import UiTextarea from './UiTextarea.vue'

describe('UiField', () => {
  it('подпись, hint, error, required-звёздочка; id связывает label и слот', () => {
    const w = mount(UiField, {
      props: { label: 'Имя пула', hint: 'латиница и дефисы', error: 'занято', required: true },
      slots: { default: (scope: { id: string }) => h('input', { id: scope.id }) },
    })
    expect(w.text()).toContain('Имя пула')
    expect(w.text()).toContain('латиница и дефисы')
    expect(w.find('.err').text()).toBe('занято')
    expect(w.find('.req').exists()).toBe(true)
    const id = w.find('label').attributes('for')
    expect(id).toBeTruthy()
    expect(w.find('input').attributes('id')).toBe(id)
  })

  it('tooltip идёт в title подписи', () => {
    const w = mount(UiField, { props: { label: 'x', tooltip: 'что это' } })
    expect(w.find('label').attributes('title')).toBe('что это')
  })
})

describe('UiSwitch', () => {
  it('переключение эмитит update:modelValue', async () => {
    const w = mount(UiSwitch, { props: { modelValue: false, label: 'включён' } })
    await w.find('input').setValue(true)
    expect(w.emitted('update:modelValue')![0]).toEqual([true])
  })

  it('hint под подписью, tooltip в title строки, aria-label у инпута', () => {
    const w = mount(UiSwitch, { props: { modelValue: true, label: 'группа', hint: 'правила в работе', tooltip: 'тумблер группы' } })
    expect(w.find('.uihint').text()).toBe('правила в работе')
    expect(w.attributes('title')).toBe('тумблер группы')
    expect(w.find('input').attributes('aria-label')).toBe('группа')
  })

  it('disabled: инпут задизейблен и строка приглушена', () => {
    const w = mount(UiSwitch, { props: { modelValue: false, label: 'x', disabled: true } })
    expect(w.find('input').attributes('disabled')).toBeDefined()
    expect(w.find('.uiswitch').classes()).toContain('off')
  })
})

describe('UiInput', () => {
  it('ввод эмитит update:modelValue, валидатор ставит и снимает ошибку', async () => {
    const w = mount(UiInput, {
      props: { modelValue: '', label: 'порт', validator: (v) => (/^\d+$/.test(v) ? null : 'только цифры') },
    })
    expect(w.find('.err').exists()).toBe(false)
    await w.find('input').setValue('abc')
    expect(w.emitted('update:modelValue')![0]).toEqual(['abc'])
    expect(w.find('.err').text()).toBe('только цифры')
    await w.find('input').setValue('8090')
    expect(w.find('.err').exists()).toBe(false)
  })

  it('mono даёт класс, placeholder и hint на месте', () => {
    const w = mount(UiInput, { props: { modelValue: '', label: 'токен', mono: true, placeholder: '0000', hint: 'от админки роутера' } })
    expect(w.find('input').classes()).toContain('mono')
    expect(w.find('input').attributes('placeholder')).toBe('0000')
    expect(w.text()).toContain('от админки роутера')
  })

  it('пароль: глаз переключает тип, без глаза - обычный text', async () => {
    const w = mount(UiInput, { props: { modelValue: 's3cret', label: 'пароль', type: 'password' } })
    expect(w.find('input').attributes('type')).toBe('password')
    await w.find('.peye').trigger('click')
    expect(w.find('input').attributes('type')).toBe('text')
    const plain = mount(UiInput, { props: { modelValue: '', label: 'имя' } })
    expect(plain.find('.peye').exists()).toBe(false)
  })
})

describe('UiTextarea', () => {
  it('ввод эмитит значение, rows применяется', async () => {
    const w = mount(UiTextarea, { props: { modelValue: '', label: 'правила', rows: 7 } })
    expect(w.find('textarea').attributes('rows')).toBe('7')
    await w.find('textarea').setValue('8.8.8.8/32')
    expect(w.emitted('update:modelValue')![0]).toEqual(['8.8.8.8/32'])
  })
})

const OPTS: UiSelectOption[] = [
  { value: 'proton', label: 'proton', hint: 'пул по умолчанию', count: 3 },
  { value: 'warp', label: 'warp' },
]

describe('UiCheckbox', () => {
  it('с подписью: раскладка как у свитча, клик эмитит, hint на месте', async () => {
    const w = mount(UiCheckbox, { props: { modelValue: false, label: 'показать дубликаты', hint: 'правила из других групп' } })
    expect(w.find('.lbl').text()).toBe('показать дубликаты')
    expect(w.find('.uihint').text()).toBe('правила из других групп')
    await w.find('input').setValue(true)
    expect(w.emitted('update:modelValue')![0]).toEqual([true])
  })

  it('без label - голый бокс (строки таблиц), tooltip в title, aria по tooltip', () => {
    const w = mount(UiCheckbox, { props: { modelValue: false, tooltip: 'правило включено' } })
    expect(w.find('.lbl').exists()).toBe(false)
    expect(w.attributes('title')).toBe('правило включено')
    expect(w.find('input').attributes('aria-label')).toBe('правило включено')
  })

  it('disabled: бокс приглушён, инпут задизейблен', () => {
    const w = mount(UiCheckbox, { props: { modelValue: false, label: 'x', disabled: true } })
    expect(w.classes()).toContain('off')
    expect(w.find('input').attributes('disabled')).toBeDefined()
  })
})

describe('UiSelect', () => {
  it('показывает выбранный пункт, раскрытие и выбор эмитят значение', async () => {
    const w = mount(UiSelect, { props: { modelValue: 'proton', options: OPTS, label: 'пул' } })
    expect(w.find('.psel-btn').text()).toContain('proton')
    expect(w.find('.psel-list').exists()).toBe(false)
    await w.find('.psel-btn').trigger('click')
    expect(w.find('.psel-list').exists()).toBe(true)
    expect(w.findAll('.psel-item')[0].text()).toContain('пул по умолчанию')
    expect(w.findAll('.psel-item')[0].text()).toContain('3')
    await w.findAll('.psel-item')[1].trigger('click')
    expect(w.emitted('update:modelValue')![0]).toEqual(['warp'])
    expect(w.find('.psel-list').exists()).toBe(false)
  })

  it('Escape закрывает список', async () => {
    const w = mount(UiSelect, { props: { modelValue: 'proton', options: OPTS, label: 'пул' } })
    await w.find('.psel-btn').trigger('click')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await w.vm.$nextTick()
    expect(w.find('.psel-list').exists()).toBe(false)
  })

  it('клик вне закрывает список', async () => {
    const w = mount(UiSelect, { props: { modelValue: 'proton', options: OPTS, label: 'пул' } })
    await w.find('.psel-btn').trigger('click')
    // jsdom не знает PointerEvent - тип события тот же, что слушает компонент
    document.body.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }))
    await w.vm.$nextTick()
    expect(w.find('.psel-list').exists()).toBe(false)
  })

  it('disabled не даёт раскрыть', async () => {
    const w = mount(UiSelect, { props: { modelValue: 'proton', options: OPTS, label: 'пул', disabled: true } })
    await w.find('.psel-btn').trigger('click')
    expect(w.find('.psel-list').exists()).toBe(false)
  })
})
