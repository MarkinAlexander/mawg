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
});
