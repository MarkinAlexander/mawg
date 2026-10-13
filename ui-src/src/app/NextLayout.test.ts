import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createRouter, createWebHashHistory } from 'vue-router'
import { nextTick } from 'vue'
import NextLayout from './NextLayout.vue'
import { TABS } from './tabs'
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

afterEach(() => {
  document.body.classList.remove('unlocked');
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
  it('рендерит все вкладки как ссылки и ведёт в legacy', async () => {
    const router = await makeRouter()
    const w = mount(NextLayout, { global: { plugins: [router] } })
    const links = w.findAll('nav a')
    expect(links.length).toBe(TABS.length + 1) // вкладки + «старая версия»
    expect(w.text()).toContain('Обзор')
    expect(links[links.length - 1].attributes('href')).toBe('#/')
    for (const t of TABS) {
      expect(links.map((l) => l.text())).toContain(t.label)
    }
    w.unmount()
  })

  it('снимает легаси-замок: body.unlocked при монтировании, обратно при размонтировании', async () => {
    const router = await makeRouter()
    // до монтирования panel.js ещё не ставил замок... а мог бы - снимаем
    document.body.classList.remove('unlocked')
    const w = mount(NextLayout, { global: { plugins: [router] } })
    expect(document.body.classList.contains('unlocked')).toBe(true)
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

  it('authNeeded (401) -> err-баннер со ссылкой в legacy', async () => {
    const router = await makeRouter()
    const w = mount(NextLayout, { global: { plugins: [router] } })
    expect(w.find('.uibanner').exists()).toBe(false)
    window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT))
    await nextTick()
    const banner = w.find('.uibanner')
    expect(banner.exists()).toBe(true)
    expect(banner.classes()).toContain('k-err')
    expect(banner.text()).toContain('требуется вход')
    expect(banner.find('a').attributes('href')).toBe('#/')
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
    // все вкладки + «старая версия»
    expect(sheet.findAll('a')).toHaveLength(TABS.length + 1)

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
