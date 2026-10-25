import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import SmartCascadeChip from './SmartCascadeChip.vue'
import type { SmartCascadeSettings, SmartCascadeState } from '../../api/types'

// Чип умного каскада (86-док): бейдж без захода в настройки, клик -
// поповер с планом (пути/триггер/возврат/антифлап) и живым состоянием.
const settings: SmartCascadeSettings = {
  via: ['warp', 'awgfree'],
  triggerMode: 'fails',
  triggerFails: 2,
  return: { mode: 'schedule', timeOfDay: ['03:00'] },
  minDwellMin: 10,
}
const state: SmartCascadeState = {
  on: true, via: 'warp', group: 'g1',
  since: '2026-10-25T10:00:00Z', nextCheck: '2026-10-26T03:00:00Z',
  lastDetail: 'прямой доступ потерян',
}

describe('SmartCascadeChip', () => {
  it('спокоен: бейдж «умный каскад», поповер - план и «ждёт блокировки»', async () => {
    const w = mount(SmartCascadeChip, { props: { settings } })
    expect(w.find('.scchip').text()).toBe('умный каскад')
    expect(w.find('.scchip').classes()).toContain('v-info')
    await w.find('.scchip').trigger('click')
    const pop = w.find('.scpop')
    expect(pop.exists()).toBe(true)
    expect(pop.text()).toContain('1. warp')
    expect(pop.text()).toContain('2. awgfree')
    expect(pop.text()).toContain('после 2 отказов подряд')
    expect(pop.text()).toContain('по расписанию 03:00')
    expect(pop.text()).toContain('ждёт блокировки пула')
    w.unmount()
  })

  it('активен: бейдж «через warp (каскад)», в поповере путь и следующая проверка', async () => {
    const w = mount(SmartCascadeChip, { props: { settings, state } })
    expect(w.find('.scchip').text()).toBe('через warp (каскад)')
    expect(w.find('.scchip').classes()).toContain('v-warn')
    await w.find('.scchip').trigger('click')
    const pop = w.find('.scpop')
    expect(pop.text()).toContain('трафик идёт через warp')
    expect(pop.text()).toContain('следующая проверка прямого:')
    expect(pop.text()).toContain('прямой доступ потерян')
    // fails-режим: исчерпание не должно значиться планом
    expect(pop.text()).not.toContain('перебора всех конфигов пула')
    w.unmount()
  })

  it('exhaust-режим без числа: дефолтные подписи (3 отказа не упоминаются)', async () => {
    const w = mount(SmartCascadeChip, {
      props: { settings: { via: ['warp'], return: { mode: 'interval', intervalH: 4 }, minDwellMin: 30 } },
    })
    await w.find('.scchip').trigger('click')
    expect(w.find('.scpop').text()).toContain('после перебора всех конфигов пула')
    expect(w.find('.scpop').text()).toContain('раз в 4 ч')
    expect(w.find('.scpop').text()).toContain('30 мин')
    w.unmount()
  })
})
