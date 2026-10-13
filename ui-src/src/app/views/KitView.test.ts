import { afterEach, describe, expect, it } from 'vitest'
import { defineComponent, h } from 'vue'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import KitView from './KitView.vue'
import { ConfirmHost } from '../../components/ui'
import { useConfirm } from '../../stores/confirm'
import { dismissToast, useToast } from '../../stores/toast'

// Демо-страница кита: smoke - рендерится и показывает все секции.
// Хосты тостов/confirm монтирует NextLayout - в тесте добавляем ConfirmHost рядом.
describe('KitView', () => {
  afterEach(() => {
    for (const t of [...useToast().toasts.list]) dismissToast(t.id)
    useConfirm().cancel()
  })

  it('рендерит секции демо', () => {
    const w = mount(KitView)
    expect(w.text()).toContain('UI-кит — демо')
    for (const section of ['Кнопки', 'Бейджи и баннеры', 'Поля', 'Карточка и табы', 'Таблица с DnD', 'Диалоги и тосты']) {
      expect(w.text()).toContain(section)
    }
  })

  it('кнопки демо пишут в стор тостов и открывают confirm', async () => {
    const w = mount(defineComponent(() => () => [h(KitView), h(ConfirmHost)]))
    // тост ok
    await w.findAll('button').find((b) => b.text() === 'тост ok')!.trigger('click')
    expect(useToast().toasts.list.map((t) => t.text)).toEqual(['тост: всё хорошо'])
    // confirm: открыть и отменить
    await w.findAll('button').find((b) => b.text() === 'confirm()')!.trigger('click')
    await nextTick()
    const dlg = w.findAll('dialog').find((d) => d.find('h3').text() === 'Подтверждение')!
    await dlg.findAll('.btnrow button')[0].trigger('click')
    await nextTick()
    expect(useToast().toasts.list.map((t) => t.text)).toEqual(['тост: всё хорошо', 'confirm: нет'])
  })
})
