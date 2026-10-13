import { describe, expect, it } from 'vitest'
import type { ComponentPublicInstance } from 'vue'
import { mount } from '@vue/test-utils'
import UiCard from './UiCard.vue'
import UiTable, { type UiTableColumn } from './UiTable.vue'
import UiTabs from './UiTabs.vue'
import { dropIndex, isBeforeHalf, reorder } from './dnd'

describe('dnd-логика', () => {
  it('reorder переставляет и зажимает индексы', () => {
    expect(reorder(['a', 'b', 'c'], 0, 2)).toEqual(['b', 'c', 'a'])
    expect(reorder(['a', 'b', 'c'], 2, 0)).toEqual(['c', 'a', 'b'])
    expect(reorder(['a', 'b', 'c'], 1, 1)).toEqual(['a', 'b', 'c'])
    expect(reorder(['a', 'b', 'c'], 0, 99)).toEqual(['b', 'c', 'a'])
    expect(reorder(['a', 'b', 'c'], -5, 0)).toEqual(['a', 'b', 'c'])
  })

  it('dropIndex: до/после цели зависит от направления хода', () => {
    // вниз: «до цели» съедает позицию, «после» - нет
    expect(dropIndex(0, 2, true)).toBe(1)
    expect(dropIndex(0, 2, false)).toBe(2)
    // вверх: «после цели» добавляет позицию
    expect(dropIndex(2, 0, false)).toBe(1)
    expect(dropIndex(2, 0, true)).toBe(0)
    // сам на себя - без перестановки
    expect(dropIndex(1, 1, false)).toBe(2)
    expect(dropIndex(1, 1, true)).toBe(1)
  })

  it('isBeforeHalf: верхняя половина цели', () => {
    expect(isBeforeHalf({ top: 100, height: 20 }, 109)).toBe(true)
    expect(isBeforeHalf({ top: 100, height: 20 }, 111)).toBe(false)
  })
})

describe('UiCard', () => {
  it('title, subtitle, слот actions справа', () => {
    const w = mount(UiCard, {
      props: { title: 'Пулы', subtitle: 'конфиги WireGuard' },
      slots: { actions: '<button>добавить</button>', default: '<p>тело</p>' },
    })
    expect(w.text()).toContain('Пулы')
    expect(w.text()).toContain('конфиги WireGuard')
    expect(w.find('h2 button').text()).toBe('добавить')
    expect(w.text()).toContain('тело')
  })

  it('collapsible: клик по стрелке эмитит update:collapsed и прячет тело', async () => {
    const w = mount(UiCard, { props: { title: 'x', collapsible: true, collapsed: false } })
    await w.find('.fold').trigger('click')
    expect(w.emitted('update:collapsed')![0]).toEqual([true])
    await w.setProps({ collapsed: true })
    expect(w.find('.body').isVisible()).toBe(false)
  })
})

describe('UiTabs', () => {
  const TABS = [
    { id: 'a', label: 'Альфа', hint: 'первая' },
    { id: 'b', label: 'Бета' },
  ]

  it('рендерит табы, активный с классом on, клик эмитит id', async () => {
    const w = mount(UiTabs, { props: { tabs: TABS, modelValue: 'a' } })
    const btns = w.findAll('.tab')
    expect(btns).toHaveLength(2)
    expect(btns[0].classes()).toContain('on')
    expect(btns[0].attributes('title')).toBe('первая')
    await btns[1].trigger('click')
    expect(w.emitted('update:modelValue')![0]).toEqual(['b'])
  })
})

interface Row {
  id: string
  name: string
}

// Дженерик <T> SFC не выводится через mount в @vue/test-utils -
// типизируем пропсы явным кастомом
type TableProps = {
  columns: UiTableColumn[]
  rows: Row[]
  rowKey: (r: Row) => string
  dnd?: boolean
  rowClass?: (r: Row) => string
}
const TTable = UiTable as unknown as new () => ComponentPublicInstance<TableProps>

const COLS: UiTableColumn[] = [
  { key: 'name', label: 'значение' },
  { key: 'id', label: 'id', width: '60px' },
]
const ROWS: Row[] = [
  { id: '1', name: 'первое' },
  { id: '2', name: 'второе' },
  { id: '3', name: 'третье' },
]

describe('UiTable', () => {
  it('колонки и строки, дефолтная ячейка = row[key]', () => {
    const w = mount(TTable, { props: { columns: COLS, rows: ROWS, rowKey: (r: Row) => r.id } })
    expect(w.findAll('th').map((c) => c.text())).toEqual(['значение', 'id'])
    expect(w.findAll('tbody tr')).toHaveLength(3)
    expect(w.find('tbody tr').text()).toContain('первое')
  })

  it('слот cell-<key> перекрывает дефолтную ячейку', () => {
    const w = mount(TTable, {
      props: { columns: COLS, rows: ROWS, rowKey: (r: Row) => r.id },
      slots: { 'cell-name': `<template #cell-name="{ row }"><b>{{ row.name }}!</b></template>` },
    })
    expect(w.find('tbody b').text()).toBe('первое!')
  })

  it('rowClass добавляется строке', () => {
    const w = mount(TTable, {
      props: { columns: COLS, rows: ROWS, rowKey: (r: Row) => r.id, rowClass: (r: Row) => (r.id === '2' ? 'dim' : '') },
    })
    expect(w.findAll('tbody tr')[1].classes()).toContain('dim')
  })

  it('dnd: грип взводит строку, drop над верхней половиной эмитит reorder', async () => {
    const w = mount(TTable, { props: { columns: COLS, rows: ROWS, rowKey: (r: Row) => r.id, dnd: true } })
    const rows = w.findAll('tbody tr')
    // pointerdown по грипу взводит draggable своей строки
    await rows[0].find('.grip').trigger('pointerdown')
    expect(rows[0].attributes('draggable')).toBe('true')
    // тянем первую строку на третью, «до» неё: jsdom даёт rect-нули,
    // поэтому верхняя половина = clientY < 0
    await rows[0].trigger('dragstart')
    await rows[2].trigger('dragover', { clientY: -1 })
    await rows[2].trigger('drop')
    expect(w.emitted('reorder')![0]).toEqual([0, 1])
    await rows[0].trigger('dragend')
    // после dragend строки снова не draggable
    expect(rows[0].attributes('draggable')).toBe('false')
  })

  it('dnd: нижняя половина цели - «после»', async () => {
    const w = mount(TTable, { props: { columns: COLS, rows: ROWS, rowKey: (r: Row) => r.id, dnd: true } })
    const rows = w.findAll('tbody tr')
    await rows[0].find('.grip').trigger('pointerdown')
    await rows[0].trigger('dragstart')
    // jsdom: getBoundingClientRect = нули, clientY >= top+height/2 = «после»
    await rows[2].trigger('dragover', { clientY: 500 })
    await rows[2].trigger('drop')
    expect(w.emitted('reorder')![0]).toEqual([0, 2])
  })
})
