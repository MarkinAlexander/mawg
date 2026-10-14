import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createRouter, createWebHashHistory } from 'vue-router'
import { nextTick } from 'vue'
import NextLayout from './NextLayout.vue'
import { TABS } from './tabs'
import { SEARCH_PLACES } from './search'
import { dialogBackReset } from '../components/ui/dialogBack'
import { UNAUTHORIZED_EVENT, inflight } from '../api/client'
import { useStatus } from '../stores/status'
import { useSystem } from '../stores/system'

// лейаут на mount стартует опросы статуса (5с/30с) и проверку обновления -
// fetch стабим по пути; немедленные ответы по умолчанию
function apiStub(routes: Record<string, unknown> = {}) {
  return vi.fn(async (url: string) => {
    const key = url.replace('/api/v1', '');
    const body = routes[key] !== undefined ? routes[key] : {};
    return new Response(JSON.stringify(body), { status: 200 });
  });
}

beforeEach(() => {
  vi.unstubAllGlobals();
  vi.stubGlobal('fetch', apiStub());
  const s = useStatus().state;
  s.loaded = false;
  s.authNeeded = false;
  s.error = '';
  s.mt = 'checking';
  s.data = null;
  useSystem().upd.info = null;
  inflight.n = 0;
});

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

describe('NextLayout', () => {
  it('рендерит все вкладки как ссылки, без ссылки на legacy', async () => {
    const router = await makeRouter()
    const w = mount(NextLayout, { global: { plugins: [router] } })
    const links = w.findAll('nav a')
    expect(links.length).toBe(TABS.length)
    expect(w.text()).toContain('Обзор')
    expect(w.text()).not.toContain('старая версия')
    for (const t of TABS) {
      expect(links.map((l) => l.text())).toContain(t.label)
    }
    w.unmount()
  })

  it('не трогает класс body.unlocked (легаси-замок удалён вместе с panel.css)', async () => {
    document.body.classList.remove('unlocked')
    const router = await makeRouter()
    const w = mount(NextLayout, { global: { plugins: [router] } })
    expect(document.body.classList.contains('unlocked')).toBe(false)
    w.unmount()
    expect(document.body.classList.contains('unlocked')).toBe(false)
  })

  it('мета-строка: загрузка -> platform | mawg X | magitrickle: доступен', async () => {
    vi.stubGlobal('fetch', apiStub({
      '/status': { version: 'v0.6.10', platform: 'keenetic', pools: [] },
      '/magitrickle': { available: true },
    }));
    const router = await makeRouter()
    const w = mount(NextLayout, { global: { plugins: [router] } })
    expect(w.find('.meta').text()).toBe('загрузка...')
    await vi.waitFor(() => {
      expect(w.find('.meta').text()).toBe('keenetic | mawg v0.6.10 | magitrickle: доступен')
    })
    w.unmount()
  })

  it('мета-строка: ошибка опроса заменяет мету (легаси «ошибка: …»)', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('down'); }));
    const router = await makeRouter()
    const w = mount(NextLayout, { global: { plugins: [router] } })
    await vi.waitFor(() => {
      expect(w.find('.meta').text()).toBe('ошибка: панель недоступна')
    })
    w.unmount()
  })

  it('authNeeded (401) -> err-баннер без ссылки (логина в новом UI нет до Фазы 5)', async () => {
    const router = await makeRouter()
    const w = mount(NextLayout, { global: { plugins: [router] } })
    expect(w.find('.uibanner').exists()).toBe(false)
    window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT))
    await nextTick()
    const banner = w.find('.uibanner')
    expect(banner.exists()).toBe(true)
    expect(banner.classes()).toContain('k-err')
    expect(banner.text()).toContain('требуется вход')
    expect(banner.find('a').exists()).toBe(false)
    w.unmount()
  })

  it('topbar включается по in-flight запросам и гаснет', async () => {
    const router = await makeRouter()
    const w = mount(NextLayout, { global: { plugins: [router] } })
    const bar = w.find('.topbar')
    expect(bar.classes()).not.toContain('on')
    inflight.n = 2
    await nextTick()
    expect(bar.classes()).toContain('on')
    inflight.n = 0
    await nextTick()
    expect(bar.classes()).not.toContain('on')
    w.unmount()
  })

  it('бейдж обновления в шапке появляется по upd.info.update (легаси-текст)', async () => {
    useSystem().upd.info = { current: 'v0.6.10', latest: 'v0.6.11', update: true };
    const router = await makeRouter()
    const w = mount(NextLayout, { global: { plugins: [router] } })
    const b = w.find('.updbadge')
    expect(b.exists()).toBe(true)
    expect(b.text()).toBe('обновиться до v0.6.11')
    w.unmount()
  })

  it('«mawg next» - ссылка на главную новой панели (/next/)', async () => {
    const router = await makeRouter()
    const w = mount(NextLayout, { global: { plugins: [router] } })
    const brand = w.find('h1 a.brand')
    expect(brand.exists()).toBe(true)
    expect(brand.attributes('href')).toBe('#/next/')
    expect(brand.text()).toContain('mawg')
    expect(brand.text()).toContain('next')
    w.unmount()
  })

  it('кнопка-лупа в шапке открывает поиск, результат ведёт на вкладку', async () => {
    const router = await makeRouter()
    const w = mount(NextLayout, { global: { plugins: [router] } })
    // грабля забытого импорта: кнопка и диалог - resolved-компоненты
    const btn = w.find('header .uibtn.v-icon')
    expect(btn.exists()).toBe(true)
    expect(btn.attributes('title')).toBe('поиск по настройкам (Ctrl+K)')
    expect(btn.find('.uiicon').exists()).toBe(true)
    expect(w.html()).not.toContain('<usearchdialog')
    expect(w.find('dialog[open]').exists()).toBe(false)

    await btn.trigger('click')
    await nextTick()
    await nextTick()
    const openDlg = w.find('dialog[open] .sbox')
    expect(openDlg.exists()).toBe(true)
    expect(w.findAll('.srow')).toHaveLength(SEARCH_PLACES.length)

    // клик по результату: диалог закрыт, маршрут сменился
    // (история реальная: закрытие диалога делает back() - ждём waitFor'ом)
    await w.findAll('.srow')[0].trigger('click')
    await vi.waitFor(() => {
      expect(w.find('dialog[open]').exists()).toBe(false)
      expect(router.currentRoute.value.path).toBe(SEARCH_PLACES[0].route)
    })
    w.unmount()
  })

  it('Ctrl+K открывает поиск, без модификатора - нет', async () => {
    const router = await makeRouter()
    const w = mount(NextLayout, { global: { plugins: [router] } })
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'k' }))
    await nextTick()
    expect(w.find('dialog[open]').exists()).toBe(false)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true }))
    await nextTick()
    await nextTick()
    expect(w.find('dialog[open] .sbox').exists()).toBe(true)
    w.unmount()
  })

  it('поиск закрывается кликом по пустому месту и «назад» (popstate)', async () => {
    vi.spyOn(window.history, 'pushState').mockImplementation(() => {})
    vi.spyOn(window.history, 'back').mockImplementation(() => {})
    dialogBackReset()
    const router = await makeRouter()
    const w = mount(NextLayout, { global: { plugins: [router] } })
    const btn = w.find('header .uibtn.v-icon')
    await btn.trigger('click')
    await nextTick()
    await nextTick()
    expect(w.find('dialog[open] .sbox').exists()).toBe(true)

    // клик с target=dialog - пустое место (backdrop)
    await w.find('dialog[open]').trigger('click')
    await nextTick()
    expect(w.find('dialog[open]').exists()).toBe(false)
    // back() застаблен - счётчик selfPops не погасится сам, чистим
    dialogBackReset()

    // мусорный тег истории от ранних тестов снимаем - тег ставится стабом
    window.history.replaceState(null, '')
    // переоткрытие - «назад» закрывает
    await btn.trigger('click')
    await nextTick()
    await nextTick()
    expect(w.find('dialog[open] .sbox').exists()).toBe(true)
    window.dispatchEvent(new PopStateEvent('popstate'))
    await nextTick()
    expect(w.find('dialog[open]').exists()).toBe(false)
    // история чиста: back-записи диалога не остаются
    expect((window.history.state as { mawgDlg?: number } | null)?.mawgDlg).toBeUndefined()
    dialogBackReset()
    w.unmount()
  })

  it('мобильное меню: FAB открывает шторку со вкладками, клик по вкладке/фону/Esc закрывают', async () => {
    const router = await makeRouter()
    const w = mount(NextLayout, { global: { plugins: [router] } })
    const fab = w.find('.menu-fab')
    expect(fab.exists()).toBe(true)
    expect(fab.attributes('aria-label')).toBe('меню')
    expect(fab.attributes('aria-expanded')).toBe('false')
    // шторка закрыта (v-if)
    expect(w.find('.sheet').exists()).toBe(false)

    await fab.trigger('click')
    expect(fab.attributes('aria-expanded')).toBe('true')
    const sheet = w.find('.sheet')
    expect(sheet.exists()).toBe(true)
    // все вкладки, ссылки на legacy больше нет
    expect(sheet.findAll('a')).toHaveLength(TABS.length)

    // клик по вкладке закрывает
    await sheet.findAll('a')[0].trigger('click')
    expect(w.find('.sheet').exists()).toBe(false)
    expect(fab.attributes('aria-expanded')).toBe('false')

    // фон и Esc тоже закрывают
    await fab.trigger('click')
    await w.find('.sheet-backdrop').trigger('click')
    expect(w.find('.sheet').exists()).toBe(false)
    await fab.trigger('click')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await nextTick()
    expect(w.find('.sheet').exists()).toBe(false)
    w.unmount()
  })

  it('жест от правого края влево открывает меню (только на мобильной ширине, срыв по вертикали)', async () => {
    const router = await makeRouter()
    // jsdom: PointerEvent нет (GOTCHAS - MouseEvent с тем же типом),
    // matchMedia всегда false - замыкаем на innerWidth
    window.matchMedia = ((q: string) =>
      ({ matches: /max-width:\s*820px/.test(q) && window.innerWidth <= 820 })) as typeof window.matchMedia
    const w = mount(NextLayout, { global: { plugins: [router] } })
    const pe = (type: string, x: number, y: number) =>
      new MouseEvent(type, { clientX: x, clientY: y, bubbles: true })
    const innerWidth = window.innerWidth
    const setW = (v: number) => Object.defineProperty(window, 'innerWidth', { value: v, configurable: true })

    // десктопная ширина: жест игнорируется
    setW(1280)
    document.dispatchEvent(pe('pointerdown', innerWidth - 5, 300))
    document.dispatchEvent(pe('pointermove', innerWidth - 90, 300))
    expect(w.find('.sheet').exists()).toBe(false)

    // мобильная ширина: тянем от правого края влево - открывается
    // (DOM патчится асинхронно - nextTick после открывающего свайпа)
    setW(390)
    document.dispatchEvent(pe('pointerdown', 385, 300))
    document.dispatchEvent(pe('pointermove', 360, 300))
    expect(w.find('.sheet').exists()).toBe(false) // 25px < порога 40
    document.dispatchEvent(pe('pointermove', 320, 300))
    await nextTick()
    expect(w.find('.sheet').exists()).toBe(true)
    document.dispatchEvent(pe('pointerup', 320, 300))
    await w.find('.sheet-backdrop').trigger('click')

    // срыв по вертикали (скролл) не открывает
    document.dispatchEvent(pe('pointerdown', 385, 300))
    document.dispatchEvent(pe('pointermove', 340, 380))
    document.dispatchEvent(pe('pointermove', 300, 420))
    expect(w.find('.sheet').exists()).toBe(false)
    document.dispatchEvent(pe('pointerup', 300, 420))

    // старт не от края - не открывает
    document.dispatchEvent(pe('pointerdown', 200, 300))
    document.dispatchEvent(pe('pointermove', 120, 300))
    expect(w.find('.sheet').exists()).toBe(false)
    document.dispatchEvent(pe('pointerup', 120, 300))
    setW(innerWidth)
    w.unmount()
  })
})
