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
})
