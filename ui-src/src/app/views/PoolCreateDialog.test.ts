import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import PoolCreateDialog from './PoolCreateDialog.vue'
import { useCaptcha } from '../../stores/captcha'
import { useConfirm } from '../../stores/confirm'
import { useStatus } from '../../stores/status'
import { useToast } from '../../stores/toast'
import type { Status } from '../../api/types'

type ReqInit = { method?: string; body?: string };

function stub(routes: Record<string, unknown>) {
  return vi.fn(async (url: string, init?: ReqInit) => {
    const key = Object.keys(routes).find((k) => (url as string).endsWith(k));
    if (key) {
      const v = routes[key];
      const body = typeof v === 'function' ? (v as (init?: ReqInit) => unknown)(init) : v;
      return new Response(JSON.stringify(body), { status: 200 });
    }
    return new Response('{}', { status: 200 });
  });
}

const statusFor = (over: Partial<Status> = {}): Status => ({
  version: 'v', platform: 'keenetic', backendName: 'keenetic', authEnabled: false,
  nativeAWG3: false, mtSources: true,
  pools: [{ name: 'warp', platform: 'keenetic', device: 'nwg9', slot: 'nwg9', mode: 'up', activeFile: '', activeEndpoint: '', lastResult: '', lastError: '', rotations: 0, disabled: false, consecFails: 0, settings: {}, configs: [], premium: { imported: false, countries: [] }, protos: [] }],
  engine: { available: true, running: true, version: '1.11', mode: 'own', tuns: [{ pool: 'warp', nodes: 3, probePort: 2282 }] },
  ...over,
});

async function makeDialog(routes: Record<string, unknown>, over: Partial<Status> = {}) {
  useStatus().state.data = statusFor(over);
  const f = stub(routes);
  vi.stubGlobal('fetch', f);
  const dlg = mount(PoolCreateDialog, { props: { open: false } });
  await dlg.setProps({ open: true });
  await nextTick();
  return { dlg, f };
}

const btn = (w: ReturnType<typeof mount>, text: string) =>
  w.findAll('button').filter((b) => b.text() === text);

beforeEach(() => {
  vi.unstubAllGlobals();
  useStatus().stop();
  useStatus().state.data = null;
  useConfirm().cancel();
  useToast().toasts.list.splice(0);
  useCaptcha().cancel();
});

describe('PoolCreateDialog', () => {
  it('режим конфигов: слоты грузятся, занятые пулами спрятаны, Создать -> POST /pools', async () => {
    const { dlg, f } = await makeDialog({
      '/slots': { slots: [
        { id: 'nwg9', device: 'nwg9', description: '', linkUp: false, connected: false, managed: true },
        { id: 'nwg0', device: 'nwg0', description: '', linkUp: false, connected: false, managed: false },
        { id: 'nwg1', device: 'nwg1', description: 'провайдер', linkUp: true, connected: true, managed: false },
      ] },
    });
    await vi.waitFor(() => expect(dlg.text()).toContain('nwg0'));
    expect(dlg.text()).not.toContain('nwg9'); // занят пулом warp - спрятан
    expect(dlg.find('.psel-btn').text()).toContain('свободен'); // по умолчанию свободный

    await dlg.find('input').setValue('newpool');
    await btn(dlg, 'Создать')[0].trigger('click');
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools') && c[1]?.method === 'POST');
      expect(JSON.parse(post![1]!.body as string)).toEqual({ name: 'newpool', fallback: 'direct', keeneticSlot: 'nwg0' });
    });
    dlg.unmount();
  });

  it('режим ссылки: inspect с подсказкой, Создать -> POST /pools/from-source с source', async () => {
    const { dlg, f } = await makeDialog({
      '/slots': { slots: [{ id: 'nwg0', device: 'nwg0', description: '', linkUp: false, connected: false, managed: false }] },
      '/links/inspect': { native: 2, engine: 1, types: ['vless'], intervalHours: 6 },
    });
    await btn(dlg, 'ссылка или подписка')[0].trigger('click');
    await nextTick();
    const ta = dlg.find('textarea');
    await ta.setValue('https://example.com/sub');
    // debounce 500мс
    await vi.waitFor(() => expect(dlg.text()).toContain('WG/AWG узлов: 2'), { timeout: 3000 });
    expect(dlg.text()).toContain('прокси-узлов: 1 (vless)');

    await dlg.find('input').setValue('sub');
    await btn(dlg, 'Создать')[0].trigger('click');
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/from-source'));
      expect(JSON.parse(post![1]!.body as string)).toEqual({ name: 'sub', fallback: 'direct', keeneticSlot: 'nwg0', source: 'https://example.com/sub' });
    });
    // план показан, кнопка сменилась на «Закрыть»
    await vi.waitFor(() => expect(dlg.text()).toContain('Пул из источника'));
    dlg.unmount();
  });

  it('Amnezia Free: капча через общий хост, решение -> повторный запрос, план', async () => {
    let captchaAsked = 0;
    const { dlg } = await makeDialog({
      '/pools/amnezia-free/captcha': (init?: { method?: string; body?: string }) => {
        if (init?.body && String(init.body).includes('captchaSolution')) {
          return { pool: 'free', tun: 'tun1', applied: 2, probeOk: true, probeMs: 100 };
        }
        captchaAsked++;
        return { captchaRequired: true, captcha: { id: 'cap-' + captchaAsked, image: 'AAAA' } };
      },
      '/pools/amnezia-free': () => ({ captchaRequired: true, captcha: { id: 'cap-0', image: 'AAAA' } }),
    });
    await btn(dlg, 'Amnezia Free без аккаунта')[0].trigger('click');
    await nextTick();
    expect(dlg.text()).toContain('Пул создаётся выключенным');

    await dlg.find('input').setValue('myfree');
    await btn(dlg, 'Создать')[0].trigger('click');
    // капча открылась в хосте
    await vi.waitFor(() => expect(useCaptcha().state.open).toBe(true));
    expect(dlg.find('.capimg').exists()).toBe(false); // капча НЕ внутри диалога создания
    useCaptcha().answer('777');
    await vi.waitFor(() => expect(dlg.text()).toContain('Пул из источника'));
    expect(dlg.text()).toContain('Пул free создан: tun-интерфейс tun1, узлов 2 · проба: ok (100ms).');
    dlg.unmount();
  });

  it('+ слот: ndm отдаёт слот не сразу - ретраи добирают и выбирают', async () => {
    let created = 0;
    let getsAfterCreate = 0;
    const { dlg } = await makeDialog({
      '/slots/create': () => { created++; return { slot: 'Wireguard2', device: 'nwg2' }; },
      '/slots': () => {
        if (!created) return { slots: [{ id: 'nwg1', device: 'nwg1', description: '', linkUp: false, connected: false, managed: false }] };
        getsAfterCreate++;
        const list = [{ id: 'nwg1', device: 'nwg1', description: '', linkUp: false, connected: false, managed: false }];
        // ndm применяет создание асинхронно: первый перезапрос ещё без слота
        if (getsAfterCreate >= 2) list.push({ id: 'Wireguard2', device: 'nwg2', description: '', linkUp: false, connected: false, managed: true });
        return { slots: list };
      },
    });
    await vi.waitFor(() => expect(dlg.find('.psel-btn').text()).toContain('nwg1'));
    await btn(dlg, '+ слот')[0].trigger('click');
    await vi.waitFor(() => expect(dlg.find('.psel-btn').text()).toContain('Wireguard2'), { timeout: 4000 });
    await vi.waitFor(() => expect(useToast().toasts.list.some((t) => t.text.includes('и выбран для нового пула'))).toBe(true));
    dlg.unmount();
  });

  it('+ слот: список так и не обновился - слот показывается сразу (оптимистично)', async () => {
    const { dlg } = await makeDialog({
      '/slots/create': () => ({ slot: 'Wireguard2', device: 'nwg2' }),
      '/slots': { slots: [{ id: 'nwg1', device: 'nwg1', description: '', linkUp: false, connected: false, managed: false }] },
    });
    await vi.waitFor(() => expect(dlg.find('.psel-btn').text()).toContain('nwg1'));
    await btn(dlg, '+ слот')[0].trigger('click');
    await vi.waitFor(() => expect(dlg.find('.psel-btn').text()).toContain('Wireguard2'), { timeout: 4000 });
    await vi.waitFor(() => expect(useToast().toasts.list.some((t) => t.text.includes('и выбран для нового пула'))).toBe(true));
    dlg.unmount();
  });

  it('занятый слот в конфиг-режиме: подтверждение перед созданием', async () => {
    const { dlg } = await makeDialog({
      '/slots': { slots: [{ id: 'nwg1', device: 'nwg1', description: 'провайдер', linkUp: true, connected: true, managed: false }] },
    });
    await vi.waitFor(() => expect(dlg.find('.psel-btn').text()).toContain('занят'));
    await dlg.find('input').setValue('p2');
    await btn(dlg, 'Создать')[0].trigger('click');
    await vi.waitFor(() => expect(useConfirm().state.open).toBe(true));
    expect(useConfirm().state.text).toContain('уже используется');
    useConfirm().ok();
    dlg.unmount();
  });

  it('openwrt + vless-ссылка: вопроса о протоколе нет (движок-путь)', async () => {
    const { dlg, f } = await makeDialog({
      '/links/inspect': { native: 0, engine: 1, types: ['vless'] },
    }, { platform: 'openwrt' });
    await btn(dlg, 'ссылка или подписка')[0].trigger('click');
    await nextTick();
    // до разбора селекта нет и после разбора (только движковые узлы) - нет
    expect(dlg.text()).not.toContain('Протокол (OpenWrt)');
    await dlg.find('textarea').setValue('vless://uuid@1.2.3.4:443?security=tls#node');
    await vi.waitFor(() => expect(dlg.text()).toContain('прокси-узлов: 1 (vless)'), { timeout: 3000 });
    expect(dlg.text()).not.toContain('Протокол (OpenWrt)');

    await dlg.find('input').setValue('vl');
    await btn(dlg, 'Создать')[0].trigger('click');
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/from-source'));
      expect(JSON.parse(post![1]!.body as string)).toEqual({
        name: 'vl', fallback: 'direct', openwrtProto: 'wireguard', source: 'vless://uuid@1.2.3.4:443?security=tls#node',
      });
    });
    dlg.unmount();
  });

  it('mihomo установлен + vless: селектор ядра виден, mihomo уходит в from-source', async () => {
    const { dlg, f } = await makeDialog({
      '/links/inspect': { native: 0, engine: 1, types: ['vless'] },
    }, { platform: 'openwrt', engines: { mihomo: { available: true, running: true, version: 'v1.19.33' } } });
    await btn(dlg, 'ссылка или подписка')[0].trigger('click');
    await nextTick();
    await dlg.find('textarea').setValue('vless://uuid@1.2.3.4:443?security=tls#node');
    await vi.waitFor(() => expect(dlg.text()).toContain('прокси-узлов: 1 (vless)'), { timeout: 3000 });
    await vi.waitFor(() => expect(dlg.text()).toContain('Ядро движка'));
    const coreBtn = dlg.findAll('.psel-btn').find((b) => b.text() === 'sing-box');
    expect(coreBtn).toBeDefined();
    await coreBtn!.trigger('click');
    await vi.waitFor(() => expect(dlg.find('.psel-list').exists()).toBe(true));
    const opt = dlg.findAll('.psel-item').find((o) => o.text().includes('mihomo'));
    await opt!.trigger('click');
    await nextTick();
    await dlg.find('input').setValue('mh');
    await btn(dlg, 'Создать')[0].trigger('click');
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/from-source'));
      expect(post).toBeTruthy();
      expect(JSON.parse(post![1]!.body as string)).toMatchObject({ engineCore: 'mihomo' });
    });
    dlg.unmount();
  });

  it('mihomo не установлен: селектора ядра нет при движковых узлах', async () => {
    const { dlg } = await makeDialog({
      '/links/inspect': { native: 0, engine: 1, types: ['vless'] },
    }, { platform: 'openwrt' });
    await btn(dlg, 'ссылка или подписка')[0].trigger('click');
    await nextTick();
    await dlg.find('textarea').setValue('vless://uuid@1.2.3.4:443?security=tls#node');
    await vi.waitFor(() => expect(dlg.text()).toContain('прокси-узлов: 1 (vless)'), { timeout: 3000 });
    expect(dlg.text()).not.toContain('Ядро движка');
    dlg.unmount();
  });

  it('openwrt + amneziawg-ссылка: селект появился с предвыбором amneziawg', async () => {
    const { dlg } = await makeDialog({
      '/links/inspect': { native: 1, engine: 0, types: ['amneziawg'] },
    }, { platform: 'openwrt' });
    await btn(dlg, 'ссылка или подписка')[0].trigger('click');
    await nextTick();
    expect(dlg.text()).not.toContain('Протокол (OpenWrt)'); // до разбора вопроса нет
    await dlg.find('textarea').setValue('amneziawg://key@1.2.3.4:51820');
    await vi.waitFor(() => expect(dlg.text()).toContain('Протокол (OpenWrt)'), { timeout: 3000 });
    const sel = dlg.findAll('.psel-btn').find((b) => b.text().includes('amneziawg'));
    expect(sel).toBeDefined(); // предвыбор из типов узлов
    dlg.unmount();
  });

  it('движка нет + vless-узлы: предложение установки в потоке -> POST /system/install', async () => {
    const { dlg, f } = await makeDialog({
      '/links/inspect': { native: 0, engine: 1, types: ['vless'] },
      '/system/check': { platform: 'openwrt', items: [
        { id: 'singbox-lx', title: 'ядро sing-box', installed: false, version: '',
          action: 'install', confirm: 'Скачать и установить ядро sing-box-lx?', note: '' },
      ] },
      '/system/install': { ok: true, output: 'установлен: sing-box-lx' },
    }, { platform: 'openwrt', engine: undefined });
    await btn(dlg, 'ссылка или подписка')[0].trigger('click');
    await nextTick();
    await dlg.find('textarea').setValue('vless://uuid@1.2.3.4:443?security=tls#node');
    await vi.waitFor(() => expect(dlg.text()).toContain('нужен движок sing-box'), { timeout: 3000 });

    await btn(dlg, 'Установить ядро sing-box-lx')[0].trigger('click');
    // диалог установки: предмет свежий (перечитан /system/check), дефолт plain
    // (маркер «Профиль сборки» - у внешнего диалога в баннере тоже есть
    // «ядро sing-box-lx», им не matcher'ить)
    const lxdlg = await vi.waitFor(() => {
      const d = dlg.findAll('dialog').find((x) => x.text().includes('Профиль сборки'));
      expect(d).toBeDefined();
      return d!;
    });
    expect(lxdlg.text()).toContain('Скачать и установить ядро sing-box-lx?');
    await btn(dlg, 'Установить').at(-1)!.trigger('click');
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/system/install'));
      expect(JSON.parse(post![1]!.body as string)).toEqual({ id: 'singbox-lx', source: 'extended', flavor: 'plain', backup: false });
    });
    await vi.waitFor(() => expect(dlg.text()).toContain('Установлено'));
    expect(useToast().toasts.list.some((t) => t.text.includes('Ядро sing-box-lx установлено'))).toBe(true);
    dlg.unmount();
  });

  it('free без движка: предложение установки видно сразу', async () => {
    const { dlg } = await makeDialog({}, { engine: undefined });
    await btn(dlg, 'Amnezia Free без аккаунта')[0].trigger('click');
    await nextTick();
    expect(dlg.text()).toContain('нужен движок sing-box');
    expect(btn(dlg, 'Установить ядро sing-box-lx').length).toBeGreaterThan(0);
    dlg.unmount();
  });
});
