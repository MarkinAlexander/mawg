import { describe, expect, it, vi } from 'vitest'
import { h } from 'vue'
import { mount } from '@vue/test-utils'
import UiCheckbox from './UiCheckbox.vue'
import UiField from './UiField.vue'
import UiOrderList from './UiOrderList.vue'
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

  it('number: свои стрелки вместо нативного спиннера, клики шагают значение', async () => {
    const w = mount(UiInput, { props: { modelValue: '8090', label: 'порт', type: 'number' } })
    expect(w.findAll('.pnum-btn')).toHaveLength(2)
    await w.findAll('.pnum-btn')[0].trigger('click') // больше
    expect(w.emitted('update:modelValue')!.at(-1)).toEqual(['8091'])
    await w.setProps({ modelValue: '8091' })
    await w.findAll('.pnum-btn')[1].trigger('click') // меньше
    expect(w.emitted('update:modelValue')!.at(-1)).toEqual(['8090'])
    // step
    const w2 = mount(UiInput, { props: { modelValue: '10', label: 'мс', type: 'number', step: 5 } })
    await w2.findAll('.pnum-btn')[0].trigger('click')
    expect(w2.emitted('update:modelValue')!.at(-1)).toEqual(['15'])
    // у текстового поля степпера нет
    const t = mount(UiInput, { props: { modelValue: 'x', label: 'адрес' } })
    expect(t.find('.pnum').exists()).toBe(false)
  })

  it('крестик очистки: пусто - нет, с текстом - клик очищает; number/password без крестика', async () => {
    const w = mount(UiInput, { props: { modelValue: '', label: 'адрес', placeholder: '0.0.0.0' } })
    expect(w.find('.pclr').exists()).toBe(false)
    await w.setProps({ modelValue: '192.168.0.5' })
    expect(w.find('.pclr').exists()).toBe(true)
    await w.find('.pclr').trigger('click')
    expect(w.emitted('update:modelValue')!.at(-1)).toEqual([''])

    const num = mount(UiInput, { props: { modelValue: '8090', label: 'порт', type: 'number' } })
    expect(num.find('.pclr').exists()).toBe(false)
    const pw = mount(UiInput, { props: { modelValue: 'secret', label: 'пароль', type: 'password' } })
    expect(pw.find('.pclr').exists()).toBe(false)
    expect(pw.find('.peye').exists()).toBe(true)
  })

  it('bare-режим: без label - только тумблер, aria-label из tooltip', () => {
    const w = mount(UiSwitch, { props: { modelValue: true, tooltip: 'выключить запись' } })
    expect(w.find('.uiswitch').classes()).toContain('bare')
    expect(w.find('.text').exists()).toBe(false)
    expect(w.find('.knob').exists()).toBe(true)
    expect(w.find('input').attributes('aria-label')).toBe('выключить запись')
    // семантика включено: отмеченный тумблер
    expect(w.find('input').element.checked).toBe(true)
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

  it('при замере геометрии список в координатах экрана (fixed, не скроллит модалку); скролл закрывает', async () => {
    const w = mount(UiSelect, { props: { modelValue: 'proton', options: OPTS, label: 'пул' } })
    const btn = w.find('.psel-btn').element as HTMLElement
    btn.getBoundingClientRect = () =>
      ({ top: 400, bottom: 430, left: 40, right: 360, width: 320, height: 30, x: 40, y: 400, toJSON: () => ({}) } as DOMRect)
    vi.stubGlobal('innerHeight', 900)
    vi.stubGlobal('innerWidth', 1280)
    await w.find('.psel-btn').trigger('click')
    await w.vm.$nextTick()
    const list = w.find('.psel-list').element as HTMLElement
    // место снизу 900-430-8=462 -> список фиксирован под кнопкой, высота по месту
    expect(list.style.position).toBe('fixed')
    expect(list.style.top).toBe('432px')
    expect(list.style.left).toBe('40px')
    expect(list.style.maxHeight).toBe('462px')
    expect(list.style.minWidth).toBe('320px')
    // скролл в любом контейнере (capture) закрывает: якорь уезжает
    window.dispatchEvent(new Event('scroll'))
    await w.vm.$nextTick()
    expect(w.find('.psel-list').exists()).toBe(false)
    vi.unstubAllGlobals()
    w.unmount()
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

describe('UiOrderList', () => {
  const opts: UiSelectOption[] = [
    { value: 'warp', label: 'warp' },
    { value: 'proton', label: 'proton' },
    { value: 'awgfree', label: 'awgfree' },
  ]
  const btn = (w: ReturnType<typeof mount>, text: string) =>
    w.findAll('button').filter((b) => b.text().trim() === text)
  const pickOpt = async (w: ReturnType<typeof mount>, label: string) => {
    await w.findAll('.psel-btn').filter((b) => b.text() === label || b.text() === '—')[0].trigger('click')
    await w.findAll('.psel-item').filter((o) => o.text().includes(label))[0].trigger('click')
  }

  it('добавление из селекта, порядок правится стрелками, «убрать» удаляет; выбранные скрыты из селекта', async () => {
    const w = mount(UiOrderList, { props: { modelValue: [], options: opts, emptyHint: 'пусто', orderHint: 'порядок = приоритет' } })
    expect(w.text()).toContain('пусто')
    expect(w.text()).toContain('порядок = приоритет')
    // добавить warp, затем proton
    await pickOpt(w, 'warp')
    await btn(w, 'добавить')[0].trigger('click')
    await w.setProps({ modelValue: ['warp'] }) // v-model round-trip
    await pickOpt(w, 'proton')
    await btn(w, 'добавить')[0].trigger('click')
    let emits = w.emitted('update:modelValue')!
    expect(emits[emits.length - 1][0]).toEqual(['warp', 'proton'])
    await w.setProps({ modelValue: ['warp', 'proton'] })
    // порядок: proton вверх
    const rows = w.findAll('.row')
    await rows[1].findAll('button').filter((b) => b.text().trim() === '↑')[0].trigger('click')
    emits = w.emitted('update:modelValue')!
    expect(emits[emits.length - 1][0]).toEqual(['proton', 'warp'])
    await w.setProps({ modelValue: ['proton', 'warp'] })
    // убрать proton
    await w.findAll('.row')[0].findAll('button').filter((b) => b.text().trim() === 'убрать')[0].trigger('click')
    emits = w.emitted('update:modelValue')!
    expect(emits[emits.length - 1][0]).toEqual(['warp'])
    // выбранный скрыт из селекта добавления
    await w.findAll('.psel-btn')[0].trigger('click')
    const optTexts = w.findAll('.psel-item').map((o) => o.text())
    expect(optTexts.join(' ')).not.toContain('warp')
  })

  it('когда свободных нет - селект честно говорит «нет доступных», кнопка выключена', async () => {
    const w = mount(UiOrderList, { props: { modelValue: ['warp', 'proton', 'awgfree'], options: opts } })
    await w.findAll('.psel-btn')[0].trigger('click')
    expect(w.text()).toContain('нет доступных')
    expect(btn(w, 'добавить')[0].attributes('disabled')).toBeDefined()
  })
})
