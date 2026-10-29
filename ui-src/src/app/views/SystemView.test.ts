import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import SystemView from './SystemView.vue'
import { ConfirmHost, UiSwitch } from '../../components/ui'
import { dialogBackReset } from '../../components/ui/dialogBack'
import { useEvents } from '../../stores/events'
import { useStatus } from '../../stores/status'
import { useSystem } from '../../stores/system'
import { useConfirm } from '../../stores/confirm'
import type { Status, SystemItem } from '../../api/types'

const ev = (time: string, pool: string, kind: string, message: string) => ({ time, pool, kind, message });

// универсальный стаб: ключ = суффикс пути; значение - объект ответа
// или функция (init) => объект
function apiStub(routes: Record<string, unknown | ((init?: { method?: string; body?: string }) => unknown)>) {
  return vi.fn(async (url: string, init?: { method?: string; body?: string }) => {
    const key = Object.keys(routes).find((k) => url.endsWith(k));
    const v = key ? routes[key] : {};
    const body = typeof v === 'function' ? await (v as (i?: typeof init) => unknown)(init) : v;
    return new Response(JSON.stringify(body), { status: 200 });
  });
}

const defaultItems: SystemItem[] = [
  { id: 'wireguard', title: 'WireGuard', installed: true, version: '1.0', statusText: '' },
  { id: 'mt', title: 'MagiTrickle', installed: false, version: '', statusText: 'не запущен' },
  {
    id: 'singbox-lx', title: 'ядро sing-box (чужое)', installed: true, version: '1.10',
    action: 'singbox-lx-replace', actionLabel: 'Заменить на lx',
    confirm: 'Старое ядро будет заменено.', note: 'заметка',
    backupBytes: 200 * 1048576, freeBytes: 1000 * 1048576,
  },
];

const sysCheckPayload = (items = defaultItems) => ({ platform: 'keenetic', items });

const statusWithEngine = (engine: unknown) => {
  useStatus().state.data = {
    version: 'v1', platform: 'keenetic', backendName: 'keenetic',
    authEnabled: true, nativeAWG3: false, mtSources: true, pools: [], engine,
  } as Status;
};

async function makeView(routes: Record<string, unknown | ((init?: { method?: string; body?: string }) => unknown)> = {}) {
  const f = apiStub({ '/events': { events: [] }, '/system/check': sysCheckPayload(), ...routes });
  vi.stubGlobal('fetch', f);
  const w = mount(defineComponent(() => () => [h(SystemView), h(ConfirmHost)]));
  await vi.waitFor(() => expect(useSystem().state.loaded).toBe(true));
  await nextTick();
  return { w, f };
}

const btn = (w: ReturnType<typeof mount>, text: string) =>
  w.findAll('button').filter((b) => b.text() === text);

beforeEach(() => {
  vi.unstubAllGlobals();
  // confirm-диалог живёт на history (dialogBack): отложенный back()
  // предыдущего теста каскадом закрывал следующий confirm (jsdom
  // доставляет popstate с опозданием) - стабим history целиком
  vi.spyOn(window.history, 'pushState').mockImplementation(() => {});
  vi.spyOn(window.history, 'back').mockImplementation(() => {});
  dialogBackReset();
  useEvents().state.list = [];
  useEvents().stop();
  const sys = useSystem().state;
  sys.loaded = false;
  sys.data = null;
  useSystem().cores.loaded = false;
  useSystem().cores.list = [];
  useStatus().state.data = null;
  useConfirm().cancel();
});

describe('SystemView - журнал событий', () => {
  it('новые сверху, формат строки и k-цвета пяти видов', async () => {
    vi.stubGlobal('fetch', apiStub({
      '/events': { events: [
        ev('2026-10-13T10:00:01Z', 'warp', 'rotate', 'старый конфиг'),
        ev('2026-10-13T10:00:02Z', 'warp', 'applied', 'конфиг применён'),
        ev('2026-10-13T10:00:03Z', 'subs', 'fallback', 'прямой доступ'),
        ev('2026-10-13T10:00:04Z', 'magitrickle', 'magitrickle', 'группы записаны'),
        ev('2026-10-13T10:00:05Z', 'free', 'install', 'установлено'),
      ] },
    }));
    const w = mount(SystemView);
    await vi.waitFor(() => expect(w.findAll('.log > div')).toHaveLength(5));
    const rows = w.findAll('.log > div');
    // легаси slice(-80).reverse(): последняя запись сверху
    expect(rows[0].text()).toContain('установка');
    expect(rows[4].text()).toContain('ротация');
    for (const [i, kind] of ['install', 'magitrickle', 'fallback', 'applied', 'rotate'].entries()) {
      expect(rows[i].find('.k-' + kind).exists()).toBe(true);
    }
    expect(rows[0].text()).toMatch(/^\[\d{2}:\d{2}:\d{2}\] free: установка установлено$/);
    w.unmount();
  });

  it('фильтры (Ф5§7): тип из данных + поиск по пулу/тексту, счётчик «показано N из M»', async () => {
    vi.stubGlobal('fetch', apiStub({
      '/events': { events: [
        ev('2026-10-13T10:00:01Z', 'warp', 'rotate', 'старый конфиг'),
        ev('2026-10-13T10:00:02Z', 'warp', 'applied', 'конфиг применён'),
        ev('2026-10-13T10:00:03Z', 'subs', 'fallback', 'прямой доступ'),
      ] },
    }));
    const w = mount(SystemView);
    await vi.waitFor(() => expect(w.findAll('.log > div')).toHaveLength(3));
    const logCard = w.findAll('.card').find((c) => c.text().includes('Журнал событий'))!;
    // дропдаун типов собран из данных: 3 вида + «все события»
    await logCard.find('.psel-btn').trigger('click');
    expect(logCard.findAll('.psel-item')).toHaveLength(4);
    await logCard.findAll('.psel-item').filter((o) => o.text() === 'применено')[0].trigger('click');
    await nextTick();
    expect(logCard.findAll('.log > div')).toHaveLength(1);
    expect(logCard.find('.evcount').text()).toContain('показано 1 из 3');
    // фильтры пересекаются: applied + «прямой» -> пусто
    await logCard.find('.evfilters input').setValue('прямой');
    await nextTick();
    expect(logCard.findAll('.log > div')).toHaveLength(0);
    expect(logCard.find('.evcount').text()).toContain('показано 0 из 3');
    // сброс типа, поиск по пулу находит две записи warp
    await logCard.find('.psel-btn').trigger('click');
    await logCard.findAll('.psel-item').filter((o) => o.text() === 'все события')[0].trigger('click');
    await logCard.find('.evfilters input').setValue('warp');
    await nextTick();
    expect(logCard.findAll('.log > div')).toHaveLength(2);
    expect(logCard.find('.evcount').text()).toContain('показано 2 из 3');
    w.unmount();
  });

  it('хвост 80 записей (легаси slice(-80)), экранирование HTML из событий', async () => {
    const list = Array.from({ length: 85 }, (_, i) =>
      ev(`2026-10-13T00:00:${String(i % 60).padStart(2, '0')}Z`, 'p', 'rotate', 'msg' + i));
    list[84] = ev('2026-10-13T00:01:24Z', 'p', 'rotate', '<b>xss</b> & "quotes"');
    vi.stubGlobal('fetch', apiStub({ '/events': { events: list } }));
    const w = mount(SystemView);
    await vi.waitFor(() => expect(w.findAll('.log > div')).toHaveLength(80));
    const rows = w.findAll('.log > div');
    expect(rows[0].text()).toContain('<b>xss</b>'); // events[84] - сверху
    expect(rows[1].text()).toContain('msg83');
    expect(rows[79].text()).toContain('msg5');
    expect(rows[0].find('b').exists()).toBe(false);
    w.unmount();
  });

  it('опрос 15с, stop при размонтировании', async () => {
    vi.useFakeTimers();
    try {
      const f = apiStub({ '/events': { events: [ev('2026-10-13T10:00:00Z', 'p', 'rotate', 'm')] } });
      vi.stubGlobal('fetch', f);
      const w = mount(SystemView);
      await vi.advanceTimersByTimeAsync(0);
      const calls = () => f.mock.calls.filter((c) => (c[0] as string).endsWith('/events')).length;
      expect(calls()).toBe(1);
      await vi.advanceTimersByTimeAsync(15000);
      expect(calls()).toBe(2);
      await vi.advanceTimersByTimeAsync(15000);
      expect(calls()).toBe(3);
      w.unmount();
      await vi.advanceTimersByTimeAsync(60000);
      expect(calls()).toBe(3); // после unmount тишина
    } finally {
      vi.useRealTimers();
    }
  });

  it('свёртывание карточки журнала прячет её тело (UiCard - контролируемый, нужен v-model)', async () => {
    vi.stubGlobal('fetch', apiStub({ '/events': { events: [ev('2026-10-13T10:00:00Z', 'p', 'rotate', 'm')] } }));
    const w = mount(SystemView);
    await vi.waitFor(() => expect(w.findAll('.log > div')).toHaveLength(1));
    // легаси details: свёрнуто по клику, разворачивается обратно.
    // v-show гасит .body inline-стилем; isVisible() в jsdom этой среды
    // ненадёжен (врёт на предках и после снятия) - проверяем стиль.
    // На вкладке две карточки - скоупимся на журнал
    const logCard = w.findAll('.card').find((c) => c.text().includes('Журнал событий'))!;
    const bodyStyle = () => logCard.find('.body').attributes('style') ?? '';
    const fold = () => logCard.find('.fold');
    expect(bodyStyle()).not.toContain('none');
    await fold().trigger('click');
    expect(bodyStyle()).toContain('display: none');
    await fold().trigger('click');
    expect(bodyStyle()).not.toContain('none');
    w.unmount();
  });
});

describe('SystemView - карточка «Система»', () => {
  it('таблица компонентов: title+note, бейджи состояний, кнопка действия', async () => {
    const { w } = await makeView();
    const sysCard = w.findAll('.card').find((c) => c.text().includes('Проверка зависимостей'))!;
    expect(sysCard.find('.card').exists()).toBe(false); // UiCard разрешён, не <uicard>
    expect(sysCard.text()).toContain('WireGuard');
    expect(sysCard.text()).toContain('заметка'); // note под title
    expect(sysCard.text()).toContain('не запущен'); // statusText в бейдже
    expect(sysCard.findAll('.uibadge.v-ok').length).toBeGreaterThanOrEqual(1);
    expect(sysCard.findAll('.uibadge.v-err').length).toBeGreaterThanOrEqual(1);
    expect(sysCard.text()).toContain('Заменить на lx');
    w.unmount();
  });

  it('источник сборки ядра sing-box - бейдж рядом с версией (92-док: владелец путается, чьё ядро стоит)', async () => {
    let checkItems: SystemItem[] = [
      { id: 'singbox-lx', title: 'Ядро sing-box', installed: true, version: '1.14.4-lx.25', statusText: '', source: 'lx' },
    ];
    const { w } = await makeView({
      '/system/check': () => sysCheckPayload(checkItems),
    });
    const sysCard = () => w.findAll('.card').find((c) => c.text().includes('Проверка зависимостей'))!;
    expect(sysCard().findAll('.uibadge').map((b) => b.text())).toContain('lx');
    // сменился источник - бейдж перечитывается кнопкой «обновить»
    checkItems = [
      { id: 'singbox-lx', title: 'Ядро sing-box', installed: true, version: '1.14.4', statusText: '', source: 'upstream' },
    ];
    await btn(w, 'обновить')[0].trigger('click');
    await vi.waitFor(() =>
      expect(sysCard().findAll('.uibadge').map((b) => b.text())).toContain('апстрим'),
    );
    w.unmount();
  });

  it('«обновить» перечитывает /system/check', async () => {
    const { w, f } = await makeView();
    const gets = () => f.mock.calls.filter((c) => (c[0] as string).endsWith('/system/check') && (c[1]?.method || 'GET') === 'GET').length;
    const before = gets();
    await btn(w, 'обновить')[0].trigger('click');
    await vi.waitFor(() => expect(gets()).toBe(before + 1));
    w.unmount();
  });

  it('простая установка: confirm -> POST {id} -> диалог «Установлено» с выводом', async () => {
    const { w, f } = await makeView({
      '/system/check': sysCheckPayload([
        { id: 'wg', title: 'WireGuard', installed: false, version: '', action: 'wg', actionLabel: 'Поставить', confirm: 'Точно ставить?' },
      ]),
      '/system/install': () => ({ ok: true, output: 'готово: wireguard up' }),
    });
    useStatus().state.data = { version: 'v9.9', platform: 'openwrt', backendName: 'openwrt', authEnabled: false, nativeAWG3: false, mtSources: true, pools: [] } as Status;
    await btn(w, 'Поставить')[0].trigger('click');
    // openInstall перечитывает /system/check до показа confirm - ждём
    const dlg = await vi.waitFor(() => {
      const d = w.findAll('dialog').find((x) => x.find('h3').text() === 'Поставить: WireGuard');
      expect(d).toBeDefined();
      return d!;
    });
    expect(dlg.text()).toContain('Точно ставить?');
    await dlg.findAll('.btnrow button')[1].trigger('click'); // ОК
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/system/install'));
      expect(post).toBeTruthy();
      expect(JSON.parse(post![1]!.body as string)).toEqual({ id: 'wg' });
    });
    await vi.waitFor(() => expect(w.text()).toContain('Установлено'));
    expect(w.text()).toContain('v9.9 · openwrt'); // платформа/версия - первой строкой
    expect(w.text()).toContain('готово: wireguard up');
    w.unmount();
  });

  it('пока установка идёт - диалог хода, кнопка заблокирована (молчаливое ожидание пугало)', async () => {
    let release!: (v: unknown) => void;
    const pending = new Promise((res) => {
      release = res;
    });
    const { w } = await makeView({
      '/system/check': sysCheckPayload([
        { id: 'wg', title: 'WireGuard', installed: false, version: '', action: 'wg', actionLabel: 'Поставить', confirm: 'Точно ставить?' },
      ]),
      '/system/install': () => pending,
    });
    await btn(w, 'Поставить')[0].trigger('click');
    // только ОТКРЫТЫЙ диалог: закрытые остаются в DOM с тем же h3 и
    // сбивали поиск на диалог предыдущего теста
    await vi.waitFor(() => {
      expect(w.findAll('dialog').some((x) => x.find('h3').text() === 'Поставить: WireGuard' && (x.element as HTMLDialogElement).open)).toBe(true);
    });
    useConfirm().ok(); // ОК подтверждения - через стор (тот же settle(true), что клик)
    // установка «висит»: диалог хода открыт, кнопка строки заблокирована -
    // второй клик невозможен (слово владельца: молчаливое ожидание пугало)
    const rowBtn = () =>
      w.findAll('button').filter((b) => b.text().trim() === 'Поставить' && !b.element.closest('dialog'))[0];
    expect(w.text()).toContain('Идёт установка');
    release({ ok: true, output: 'готово' });
    await vi.waitFor(() => expect(w.text()).toContain('Установлено'));
    await vi.waitFor(() => expect((rowBtn().element as HTMLButtonElement).disabled).toBe(false));
    w.unmount();
  });

  it('lx-ядро: диалог с профилем и бэкапом (места хватает - включён), POST с flavor/backup', async () => {
    const { w, f } = await makeView();
    await btn(w, 'Заменить на lx')[0].trigger('click');
    const dlg = await vi.waitFor(() => {
      const d = w.findAll('dialog').find((x) => x.text().includes('ядро sing-box'));
      expect(d).toBeDefined();
      return d!;
    });
    expect(dlg.text()).toContain('Старое ядро будет заменено.');
    expect(dlg.text()).toContain('Профиль сборки');
    // свободного места хватает: галка включена и активна
    // UiSwitch разрешён компонентом (не <uiswitch>-тег - грабля забытого импорта)
    const sw = w.findComponent(UiSwitch);
    expect(sw.exists()).toBe(true);
    expect(sw.props('modelValue')).toBe(true);
    expect(sw.props('disabled')).toBe(false);
    // первый селект диалога - источник сборки, второй - профиль
    await w.findAll('dialog .psel-btn')[1].trigger('click');
    await nextTick();
    const upx = w.findAll('dialog .psel-item').find((o) => o.text().includes('сжатый'))!;
    await upx.trigger('click');
    await nextTick();
    await btn(w, 'Заменить на lx').at(-1)!.trigger('click'); // «Установить» в диалоге
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/system/install'));
      expect(JSON.parse(post![1]!.body as string)).toEqual({ id: 'singbox-lx', source: 'extended', flavor: 'upx', backup: true });
    });
    // форма гаснет, показывается прогресс установки
    await vi.waitFor(() => expect(w.text()).toContain('Идёт установка'));
    w.unmount();
  });

  it('движок sing-box: режим из status, info, кэш-варнинг, «применить» -> confirm -> POST', async () => {
    statusWithEngine({ available: true, running: true, api: false, mode: 'own', fragment: '', cacheFile: '' });
    const { w, f } = await makeView();
    expect(w.text()).toContain('ядро запущено, но не отвечает');
    expect(w.find('.uibanner').exists()).toBe(false);

    // shared + кэш на флешке -> варнинг с кнопками
    statusWithEngine({ available: true, running: false, mode: 'shared', fragment: 'merge', cacheFile: '/opt/share/cache.db' });
    await nextTick();
    expect(w.text()).toContain('ядро не запущено | merge');
    const banner = w.find('.uibanner');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('постоянный износ');

    // меняем режим через дропдаун на own и применяем: confirm -> POST
    await w.find('.psel-btn').trigger('click');
    await nextTick();
    const own = w.findAll('.psel-item').find((o) => o.text().includes('свой экземпляр'))!;
    await own.trigger('click');
    await nextTick();
    await btn(w, 'применить')[0].trigger('click');
    await nextTick();
    const dlg = w.findAll('dialog').find((d) => d.find('h3').text() === 'Сменить режим движка')!;
    await dlg.findAll('.btnrow button')[1].trigger('click'); // «Сменить»
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/singbox/mode'));
      expect(post).toBeTruthy();
      expect(JSON.parse(post![1]!.body as string)).toEqual({ mode: 'own' });
    });
    w.unmount();
  });
});

const corePayload = (over: Partial<import('../../api/types').CoreInfo> = {}) => ({
  id: 'singbox', title: 'sing-box (движок туннелей)', installed: true,
  version: '1.14.2-lx.12', lx: true, mode: 'shared', running: true,
  autostart: true, managedByMawg: true, disabled: false,
  caps: { start: false, stop: true, enable: false, disable: true, uninstall: true },
  ...over,
});

describe('SystemView - Ядра VPN (lifecycle)', () => {
  it('карточка рендерит ядро: версия/lx/режим/состояние/автозагрузка', async () => {
    const { w } = await makeView({ '/cores': { cores: [corePayload()] } });
    await vi.waitFor(() => expect(useSystem().cores.loaded).toBe(true));
    await nextTick();
    const card = w.findAll('.corerow');
    expect(card).toHaveLength(1);
    const text = card[0].text();
    expect(text).toContain('sing-box');
    expect(text).toContain('1.14.2-lx.12');
    expect(text).toContain('shared');
    expect(text).toContain('работает');
    expect(text).toContain('автозагрузка вкл');
    expect(text).toContain('Остановить'); // работает - старт не нужен
    expect(card[0].findAll('button').filter((b) => b.text() === 'Запустить')).toHaveLength(0);
    w.unmount();
  });

  it('Остановить: confirm о движковых пулах -> POST /cores/singbox/power stop', async () => {
    const { w, f } = await makeView({
      '/cores': { cores: [corePayload()] },
      '/cores/singbox/power': () => ({ core: corePayload({ running: false, disabled: true, autostart: true,
        caps: { start: true, stop: false, enable: false, disable: true, uninstall: true } }) }),
    });
    await vi.waitFor(() => expect(useSystem().cores.loaded).toBe(true));
    await nextTick();
    await btn(w, 'Остановить')[0].trigger('click');
    const dlg = await vi.waitFor(() => {
      const d = w.findAll('dialog').find((x) => x.find('h3').text() === 'Остановить sing-box (движок туннелей)');
      expect(d).toBeDefined();
      return d!;
    });
    expect(dlg.text()).toContain('движковые пулы');
    await dlg.findAll('.btnrow button')[1].trigger('click'); // ОК
    await vi.waitFor(() => {
      const call = f.mock.calls.find((c) => c[0].endsWith('/cores/singbox/power'));
      expect(call).toBeTruthy();
    });
    const call = f.mock.calls.find((c) => c[0].endsWith('/cores/singbox/power'))!;
    expect(call[1]?.method).toBe('POST');
    expect(JSON.parse(String(call[1]?.body)).action).toBe('stop');
    w.unmount();
  });

  it('own-режим: переключателя автозагрузки нет, есть пояснение; выключенный движок предлагает Запустить', async () => {
    const { w } = await makeView({
      '/cores': { cores: [corePayload({ mode: 'own', running: false, disabled: true, autostart: false,
        autostartNote: 'own-режим: ядро стартует вместе с движковыми пулами mawg',
        caps: { start: true, stop: false, enable: false, disable: false, uninstall: true } })] },
    });
    await vi.waitFor(() => expect(useSystem().cores.loaded).toBe(true));
    await nextTick();
    const row = w.findAll('.corerow')[0];
    expect(row.text()).toContain('own-режим: ядро стартует вместе с движковыми пулами');
    expect(row.findComponent(UiSwitch).exists()).toBe(false);
    expect(row.text()).toContain('выключен');
    expect(row.findAll('button').filter((b) => b.text() === 'Запустить')).toHaveLength(1);
    // кнопки ищем ВНУТРИ строки: скрытый ConfirmHost держит stale-лейбл
    // прошлого confirm, глобальный поиск ловил бы его
    expect(row.findAll('button').filter((b) => b.text() === 'Остановить')).toHaveLength(0);
    w.unmount();
  });

  it('не установлен: строка-заглушка без кнопок lifecycle', async () => {
    const { w } = await makeView({
      '/cores': { cores: [corePayload({ installed: false, version: '', lx: false, mode: '', running: false,
        note: 'не установлен (пункт singbox-lx в таблице зависимостей выше)',
        caps: { start: false, stop: false, enable: false, disable: false, uninstall: false } })] },
    });
    await vi.waitFor(() => expect(useSystem().cores.loaded).toBe(true));
    await nextTick();
    const row = w.findAll('.corerow')[0];
    expect(row.text()).toContain('не установлен');
    expect(row.findAll('button')).toHaveLength(0);
    w.unmount();
  });
});

// mihomo (88-док): второе ядро в списке, установка из релиза прямо в строке
const mihomoPayload = (over: Partial<import('../../api/types').CoreInfo> = {}) => ({
  id: 'mihomo', title: 'mihomo (Clash.Meta)', installed: false, running: false,
  managedByMawg: false, disabled: false,
  note: 'не установлен (кнопка «Установить» ставит из релиза MetaCubeX)',
  caps: { start: false, stop: false, enable: false, disable: false, uninstall: false, install: true },
  ...over,
});

describe('SystemView - Ядра VPN (mihomo)', () => {
  it('не установлен: вторая строка с кнопкой «Установить» -> confirm -> POST /system/install {id:mihomo}', async () => {
    const { w, f } = await makeView({
      '/cores': { cores: [corePayload(), mihomoPayload()] },
      '/system/install': () => ({ ok: true, output: '- установлено\n', version: 'v1.19.33' }),
    });
    await vi.waitFor(() => expect(useSystem().cores.loaded).toBe(true));
    await nextTick();
    const rows = w.findAll('.corerow');
    expect(rows).toHaveLength(2);
    const mh = rows[1];
    expect(mh.text()).toContain('mihomo (Clash.Meta)');
    expect(mh.text()).toContain('не установлен');
    const inst = mh.findAll('button').find((b) => b.text() === 'Установить');
    expect(inst).toBeDefined();
    await inst!.trigger('click');
    const dlg = await vi.waitFor(() => {
      const d = w.findAll('dialog').find((x) => x.find('h3').text() === 'Установить mihomo (Clash.Meta)');
      expect(d).toBeDefined();
      return d!;
    });
    await dlg.findAll('.btnrow button')[1].trigger('click'); // «Установить» в confirm
    await vi.waitFor(() => {
      const post = f.mock.calls.find(
        (c) => (c[0] as string).endsWith('/system/install') && String(c[1]?.body).includes('"mihomo"'));
      expect(post).toBeTruthy();
      expect(JSON.parse(String(post![1]!.body))).toEqual({ id: 'mihomo' });
    });
    // прогресс установки держит диалог занятым
    await vi.waitFor(() => expect(w.text()).toContain('Идёт установка'));
    w.unmount();
  });

  it('установлен: own-бейджи, состояние, Остановить; тумблера автозагрузки нет', async () => {
    const { w } = await makeView({
      '/cores': { cores: [corePayload(), mihomoPayload({
        installed: true, version: 'v1.19.33', mode: 'own', running: true, managedByMawg: true,
        autostartNote: 'own-режим: ядро стартует вместе с движковыми пулами mawg',
        caps: { start: false, stop: true, enable: false, disable: false, uninstall: true, install: false },
      })] },
    });
    await vi.waitFor(() => expect(useSystem().cores.loaded).toBe(true));
    await nextTick();
    const mh = w.findAll('.corerow')[1];
    const text = mh.text();
    expect(text).toContain('v1.19.33');
    expect(text).toContain('own');
    expect(text).toContain('работает');
    expect(text).toContain('own-режим: ядро стартует вместе с движковыми пулами');
    expect(mh.findComponent(UiSwitch).exists()).toBe(false);
    expect(mh.findAll('button').filter((b) => b.text() === 'Остановить')).toHaveLength(1);
    w.unmount();
  });
});

// пункт mihomo в таблице зависимостей (29.10): версия, кнопка «обновить»,
// confirm -> POST /system/install {id:mihomo, replace:true}
describe('SystemView - mihomo в таблице зависимостей', () => {
  it('строка с версией и кнопкой, установка уходит POST с replace', async () => {
    const items: SystemItem[] = [
      ...defaultItems,
      {
        id: 'mihomo', title: 'Ядро mihomo (Clash.Meta)', installed: true, version: 'v1.19.33',
        action: 'mihomo', actionLabel: 'обновить ядро',
        confirm: 'Сейчас стоит mihomo v1.19.33. mawg скачает свежий релиз.',
      },
    ];
    const { w, f } = await makeView({
      '/system/check': sysCheckPayload(items),
      '/system/install': { ok: true, output: '- digest ok\n- установлено' },
    });
    await vi.waitFor(() => expect(w.text()).toContain('Ядро mihomo (Clash.Meta)'));
    expect(w.text()).toContain('v1.19.33');
    await btn(w, 'обновить ядро')[0].trigger('click');
    await vi.waitFor(() => expect(w.text()).toContain('Сейчас стоит mihomo v1.19.33'));
    await btn(w, 'обновить ядро').at(-1)!.trigger('click'); // ОК в confirm
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/system/install') && c[1]?.method === 'POST');
      expect(post).toBeTruthy();
      expect(JSON.parse(post![1]!.body as string)).toEqual({ id: 'mihomo', replace: true });
    });
    await vi.waitFor(() => expect(w.text()).toContain('Установлено'));
    w.unmount();
  });
});
