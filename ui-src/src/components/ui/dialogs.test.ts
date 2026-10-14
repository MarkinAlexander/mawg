import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import ConfirmHost from './ConfirmHost.vue'
import ToastHost from './ToastHost.vue'
import UiDialog from './UiDialog.vue'
import { dialogBackReset } from './dialogBack'
import { useConfirm } from '../../stores/confirm'
import { dismissToast, useToast } from '../../stores/toast'

// диалоги ходят в window.history (pushState/back/popstate) - стабим и
// сбрасываем на каждый тест, чтобы всплывающие popstate не перебегали
beforeEach(() => {
  vi.spyOn(window.history, 'pushState').mockImplementation(() => {})
  vi.spyOn(window.history, 'back').mockImplementation(() => {})
  dialogBackReset()
})
afterEach(() => dialogBackReset())

// mount() без attachTo рендерит в отсоединённое дерево - dialog ищем в обёртке
function dialogEl(w: { find: (s: string) => { element: Element } }): HTMLDialogElement {
  return w.find('dialog').element as HTMLDialogElement
}

describe('UiDialog', () => {
  it('open=true открывает нативный dialog, open=false закрывает', async () => {
    const w = mount(UiDialog, { props: { open: false, title: 'диалог' } })
    expect(dialogEl(w).hasAttribute('open')).toBe(false)
    await w.setProps({ open: true })
    await nextTick()
    expect(dialogEl(w).hasAttribute('open')).toBe(true)
    await w.setProps({ open: false })
    await nextTick()
    expect(dialogEl(w).hasAttribute('open')).toBe(false)
  })

  it('title в h3, busy гасит контент, footer в .btnrow', () => {
    const w = mount(UiDialog, {
      props: { open: true, title: 'Удалить пул?', busy: true },
      slots: { default: '<p>текст</p>', footer: '<button>ОК</button>' },
    })
    expect(w.find('h3').text()).toBe('Удалить пул?')
    expect(w.find('.dcontent').classes()).toContain('busy')
    expect(w.find('.btnrow button').text()).toBe('ОК')
  })

  it('событие close (Escape/крест) эмитит update:open=false', async () => {
    const w = mount(UiDialog, { props: { open: true, title: 'x' } })
    dialogEl(w).dispatchEvent(new Event('close'))
    await nextTick()
    expect(w.emitted('update:open')![0]).toEqual([false])
  })

  it('клик по пустому месту закрывает, клик по контенту - нет', async () => {
    const w = mount(UiDialog, {
      props: { open: true, title: 'x' },
      slots: { default: '<p>текст</p>' },
    })
    // клик по контенту (target - внутри .dcontent) не закрывает
    await w.find('p').trigger('click')
    expect(w.emitted('update:open')).toBeUndefined()
    // клик с target=dialog (backdrop; паддинг панели - в .dcontent) закрывает
    await w.find('dialog').trigger('click')
    await nextTick()
    expect(w.emitted('update:open')![0]).toEqual([false])
  })

  it('busy: клик по пустому месту не закрывает', async () => {
    const w = mount(UiDialog, { props: { open: true, title: 'x', busy: true } })
    await w.find('dialog').trigger('click')
    await nextTick()
    expect(w.emitted('update:open')).toBeUndefined()
  })

  it('locked: ни пустое место, ни назад не закрывают', async () => {
    const w = mount(UiDialog, { props: { open: true, title: 'x', locked: true } })
    await w.find('dialog').trigger('click')
    await nextTick()
    expect(w.emitted('update:open')).toBeUndefined()
    window.dispatchEvent(new PopStateEvent('popstate'))
    await nextTick()
    expect(dialogEl(w).hasAttribute('open')).toBe(true)
    expect(w.emitted('update:open')).toBeUndefined()
  })

  it('открытие кладёт запись в историю, «назад» (popstate) закрывает', async () => {
    const w = mount(UiDialog, { props: { open: false, title: 'x' } })
    await w.setProps({ open: true })
    await nextTick()
    expect(window.history.pushState).toHaveBeenCalled()
    window.dispatchEvent(new PopStateEvent('popstate'))
    await nextTick()
    expect(w.emitted('update:open')![0]).toEqual([false])
    expect(dialogEl(w).hasAttribute('open')).toBe(false)
  })

  it('закрытие иными путями подчищает историю (history.back)', async () => {
    const w = mount(UiDialog, { props: { open: false, title: 'x' } })
    await w.setProps({ open: true })
    await nextTick()
    const back = window.history.back as ReturnType<typeof vi.fn>
    expect(back).not.toHaveBeenCalled()
    await w.setProps({ open: false })
    await nextTick()
    expect(back).toHaveBeenCalled()
  })

  it('стек: назад закрывает диалоги по одному, сверху вниз', async () => {
    const a = mount(UiDialog, { props: { open: true, title: 'a' } })
    const b = mount(UiDialog, { props: { open: true, title: 'b' } })
    await nextTick()
    window.dispatchEvent(new PopStateEvent('popstate'))
    await nextTick()
    expect(b.emitted('update:open')![0]).toEqual([false])
    expect(a.emitted('update:open')).toBeUndefined()
    window.dispatchEvent(new PopStateEvent('popstate'))
    await nextTick()
    expect(a.emitted('update:open')![0]).toEqual([false])
    a.unmount()
    b.unmount()
  })
})

describe('ToastHost', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => {
    for (const t of [...useToast().toasts.list]) dismissToast(t.id)
    vi.useRealTimers()
  })

  it('рендерит очередь стора, err даёт класс; клик скрывает', async () => {
    const { push } = useToast()
    const w = mount(ToastHost)
    push('всё хорошо')
    push('упало', true)
    await nextTick()
    const toasts = w.findAll('.toast')
    expect(toasts).toHaveLength(2)
    expect(toasts[1].classes()).toContain('err')
    await toasts[0].trigger('click')
    expect(w.findAll('.toast')).toHaveLength(1)
  })

  it('автоскрытие через 6 секунд делает тост из стора', async () => {
    const { push } = useToast()
    const w = mount(ToastHost)
    push('живёт 6с')
    await nextTick()
    expect(w.findAll('.toast')).toHaveLength(1)
    vi.advanceTimersByTime(6100)
    await nextTick()
    expect(w.findAll('.toast')).toHaveLength(0)
  })
})

describe('ConfirmHost', () => {
  afterEach(() => {
    useConfirm().cancel()
  })

  it('confirm() показывает title/text; ОК резолвит промис true', async () => {
    const w = mount(ConfirmHost)
    const { confirm } = useConfirm()
    const p = confirm({ title: 'Удаление', text: 'группа со всеми правилами', okLabel: 'Удалить' })
    await nextTick()
    expect(w.find('h3').text()).toBe('Удаление')
    expect(w.find('.text').text()).toContain('группа со всеми правилами')
    const btns = w.findAll('.btnrow button')
    expect(btns.map((b) => b.text())).toEqual(['Отмена', 'Удалить'])
    await btns[1].trigger('click')
    await expect(p).resolves.toBe(true)
  })

  it('Отмена резолвит false и закрывает диалог', async () => {
    const w = mount(ConfirmHost)
    const { confirm } = useConfirm()
    const p = confirm({ title: 'x', text: 'y' })
    await nextTick()
    await w.findAll('.btnrow button')[0].trigger('click')
    await expect(p).resolves.toBe(false)
    await nextTick()
    expect(dialogEl(w).hasAttribute('open')).toBe(false)
  })

  it('info-режим: без «Отмены»', async () => {
    const w = mount(ConfirmHost)
    const { confirm } = useConfirm()
    const p = confirm({ title: 'готово', text: 'сделано', info: true, okLabel: 'Понятно' })
    await nextTick()
    const btns = w.findAll('.btnrow button')
    expect(btns).toHaveLength(1)
    expect(btns[0].text()).toBe('Понятно')
    await btns[0].trigger('click')
    await expect(p).resolves.toBe(true)
  })
})
