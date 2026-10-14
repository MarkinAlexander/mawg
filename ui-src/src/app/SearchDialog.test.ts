import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createRouter, createWebHashHistory } from 'vue-router'
import { nextTick } from 'vue'
import SearchDialog from './SearchDialog.vue'
import { TABS } from './tabs'
import { SEARCH_PLACES } from './search'

// jsdom не умеет scrollIntoView (GOTCHAS) - шимим и считаем вызовы
const scrollMock = vi.fn()

beforeEach(() => {
  Element.prototype.scrollIntoView = scrollMock
  scrollMock.mockClear()
})

afterEach(() => {
  delete (Element.prototype as unknown as Record<string, unknown>).scrollIntoView
  document.body.innerHTML = '' // подложенные якоря и attachTo-обёртки
})

function makeRouter() {
  const router = createRouter({
    history: createWebHashHistory(),
    routes: [
      ...TABS.map((t) => ({ path: `/next/${t.id}`, component: { template: '<div />' } })),
      { path: '/:pathMatch(.*)*', component: { template: '<div />' } },
    ],
  })
  router.push('/next/overview')
  return router.isReady().then(() => router)
}

async function makeDlg(open = true) {
  const router = await makeRouter()
  const w = mount(SearchDialog, { props: { open }, global: { plugins: [router] } })
  return { w, router }
}

const macrotask = () => new Promise((resolve) => setTimeout(resolve, 10))

describe('SearchDialog', () => {
  it('открыт: все места реестра, метка вкладки справа, счётчик; диалог разрешён в <dialog>', async () => {
    const { w } = await makeDlg()
    // грабля забытого импорта: компонент не должен остаться кастом-элементом
    expect(w.html()).not.toContain('<usearchdialog')
    expect(w.find('dialog').exists()).toBe(true)
    const rows = w.findAll('.srow')
    expect(rows).toHaveLength(SEARCH_PLACES.length)
    expect(rows[0].text()).toContain(SEARCH_PLACES[0].title)
    expect(rows[0].text()).toContain('Группы правил')
    expect(w.find('.sfoot').text()).toBe(`совпадений: ${SEARCH_PLACES.length}`)
    w.unmount()
  })

  it('при открытии - чистый запрос и фокус в инпут', async () => {
    const router = await makeRouter()
    // attachTo: focus работает только в присоединённом дереве (jsdom);
    // nextTick между вводом и открытием - как у реального пользователя:
    // без него vnode value до/после сброса одинаков и Vue не патчит DOM
    const w = mount(SearchDialog, {
      props: { open: false },
      attachTo: document.body,
      global: { plugins: [router] },
    })
    w.find('input').setValue('каскад')
    await nextTick()
    await w.setProps({ open: true })
    await nextTick()
    await macrotask()
    expect(w.find('input').element.value).toBe('')
    expect(document.activeElement?.tagName).toBe('INPUT')
    w.unmount()
  })

  it('фильтрация по подстроке; пусто - «Ничего не найдено»', async () => {
    const { w } = await makeDlg()
    w.find('input').setValue('КАСКад')
    await nextTick()
    let rows = w.findAll('.srow')
    expect(rows).toHaveLength(1)
    expect(rows[0].text()).toContain('Каскады')
    expect(rows[0].find('mark').text()).toBe('Каскад') // подсветка совпавшего

    w.find('input').setValue('бельды')
    await nextTick()
    rows = w.findAll('.srow')
    expect(rows).toHaveLength(0)
    expect(w.find('.sempty').text()).toBe('Ничего не найдено')
    expect(w.find('.sfoot').text()).toBe('совпадений: 0')
    w.unmount()
  })

  it('Enter - закрыть, перейти на вкладку, скролл к якорю', async () => {
    const { w, router } = await makeDlg()
    // стаб-вьюхи роутера пустые - якорь подкладываем как карточка вьюхи
    const anchor = document.createElement('section')
    anchor.id = 'card-probe'
    document.body.appendChild(anchor)
    w.find('input').setValue('wan')
    await nextTick()
    expect(w.findAll('.srow')).toHaveLength(1)
    w.find('input').trigger('keydown', { key: 'Enter' })
    await macrotask()
    expect(w.emitted('update:open')).toEqual([[false]])
    expect(router.currentRoute.value.path).toBe('/next/interfaces')
    expect(scrollMock).toHaveBeenCalledWith({ behavior: 'smooth', block: 'start' })
    w.unmount()
  })

  it('клик по строке - переход (место без скролла не дёргает якорь)', async () => {
    const { w, router } = await makeDlg()
    const anchor = document.createElement('section')
    anchor.id = 'card-settings'
    document.body.appendChild(anchor)
    w.find('input').setValue('rci')
    await nextTick()
    await w.findAll('.srow')[0].trigger('click')
    await macrotask()
    expect(router.currentRoute.value.path).toBe('/next/settings')
    expect(scrollMock).toHaveBeenCalledTimes(1) // только якорь card-settings
    w.unmount()
  })

  it('стрелки двигают выбор, за конец не выходят', async () => {
    const { w } = await makeDlg()
    const rows = w.findAll('.srow')
    const inp = w.find('input')
    inp.trigger('keydown', { key: 'ArrowDown' })
    await nextTick()
    expect(rows[1].classes()).toContain('sel')
    inp.trigger('keydown', { key: 'ArrowUp' })
    await nextTick()
    expect(rows[0].classes()).toContain('sel')
    for (let i = 0; i < SEARCH_PLACES.length + 3; i++) {
      inp.trigger('keydown', { key: 'ArrowDown' })
    }
    await nextTick()
    expect(rows[rows.length - 1].classes()).toContain('sel')
    expect(rows[0].classes()).not.toContain('sel')
    w.unmount()
  })

  it('штатное закрытие нативного dialog эмитит update:open=false', async () => {
    const { w } = await makeDlg()
    w.find('dialog').trigger('close')
    await nextTick()
    expect(w.emitted('update:open')).toEqual([[false]])
    w.unmount()
  })
})
