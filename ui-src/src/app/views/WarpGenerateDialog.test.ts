import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import WarpGenerateDialog from './WarpGenerateDialog.vue'
import { useStatus } from '../../stores/status'
import type { Status } from '../../api/types'

// Диалог генератора WARP: форма (пресеты с сервера) -> POST тела ->
// результат (верdictы, ссылки WG/AWG) и честный совет zator при
// недоступности Cloudflare.

const presetsPayload = {
  domainEndpoint: 'engage.cloudflareclient.com',
  v4Blocks: ['162.159.192.0/24', '188.114.96.0/22'],
  ports: [2408, 500, 1701, 4500],
  presets: ['engage.cloudflareclient.com:2408', '162.159.192.1:2408'],
  sites: [
    { id: 'youtube', label: 'YouTube', count: 300 },
    { id: 'telegram', label: 'Telegram', count: 50 },
  ],
  domainHints: ['youtube.com'],
  limits: { count: 16 },
};

const statusFor = (): Status => ({
  version: 'v', platform: 'openwrt', backendName: 'openwrt', authEnabled: false,
  nativeAWG3: false, mtSources: true,
  pools: [], engine: null,
} as unknown as Status);

function stub(generatePlan: Record<string, unknown>, capture?: { body?: string }) {
  return vi.fn(async (url: string, init?: { method?: string; body?: unknown }) => {
    const u = url as string;
    if (u.endsWith('/warp/presets')) {
      return new Response(JSON.stringify(presetsPayload), { status: 200 });
    }
    if (u.endsWith('/warp/generate')) {
      if (capture && typeof init?.body === 'string') capture.body = init.body;
      return new Response(JSON.stringify(generatePlan), { status: 200 });
    }
    if (u.endsWith('/status')) {
      return new Response(JSON.stringify(statusFor()), { status: 200 });
    }
    return new Response('{}', { status: 200 });
  });
}

async function openDialog(generatePlan: Record<string, unknown>, capture?: { body?: string }) {
  vi.stubGlobal('fetch', stub(generatePlan, capture));
  const w = mount(WarpGenerateDialog, { props: { open: false } });
  await w.setProps({ open: true });
  await vi.waitFor(() => expect(w.text()).toContain('Сколько конфигов'));
  return w;
}

const btn = (w: ReturnType<typeof mount>, text: string) =>
  w.findAll('button').filter((b) => b.text() === text);

beforeEach(() => {
  vi.unstubAllGlobals();
  const s = useStatus();
  s.stop();
  s.state.data = null;
});

describe('WarpGenerateDialog - форма', () => {
  it('рисует поля и пресеты сайтов с сервера', async () => {
    const w = await openDialog({ pool: 'warp', added: 1, results: [] });
    expect(w.text()).toContain('Генератор WARP');
    expect(w.text()).toContain('Сколько конфигов');
    await vi.waitFor(() => expect(w.text()).toContain('YouTube (300)'));
    expect(w.text()).toContain('Telegram (50)');
    expect(w.text()).toContain('warp-generation.github.io');
    w.unmount();
  });
});

describe('WarpGenerateDialog - генерация', () => {
  it('шлёт default (random, 3) и показывает вердикты с ссылками WG/AWG', async () => {
    const capture: { body?: string } = {};
    const w = await openDialog({
      pool: 'warp', added: 2,
      results: [
        { file: 'w1.conf', endpoint: '162.159.192.7:2408', address: '172.16.0.2', ok: true, detail: 'ok (handshake 3s ago)' },
        { file: 'w2.conf', endpoint: '8.39.204.2:934', ok: false, detail: 'probe failed' },
      ],
    }, capture);
    await btn(w, 'Сгенерировать')[0].trigger('click');
    await vi.waitFor(() => expect(w.text()).toContain('Пул warp создан'));
    expect(capture.body && JSON.parse(capture.body)).toEqual({
      count: 3, mode: 'random', sites: [], domains: [],
    });
    expect(w.text()).toContain('162.159.192.7:2408');
    expect(w.text()).toContain('работает');
    expect(w.text()).toContain('не отвечает');
    const links = w.findAll('a').map((a) => a.attributes('href'));
    expect(links).toContain('/api/v1/pools/warp/configs/w1.conf');
    expect(links).toContain('/api/v1/pools/warp/configs/w1.conf?variant=awg');
    // приватный ключ наружу не выдаётся никогда
    expect(w.text()).not.toContain('PrivateKey');
    w.unmount();
  });

  it('недоступность Cloudflare - честный совет zator + сайт-генератор (AWG)', async () => {
    const w = await openDialog({
      error: 'оба адреса Cloudflare (api.devices.cloudflare.com, api.cloudflareclient.com) недоступны с вашей сети',
      unreachable: true,
      hosts: ['api.devices.cloudflare.com', 'api.cloudflareclient.com'],
      generator: 'https://warp-generation.github.io/',
    });
    await btn(w, 'Сгенерировать')[0].trigger('click');
    await vi.waitFor(() => expect(w.text()).toContain('Генерация не удалась'));
    const links = w.findAll('a').map((a) => a.attributes('href'));
    expect(links).toContain('https://github.com/AloofLibra/zator');
    expect(links).toContain('https://warp-generation.github.io/');
    expect(w.text()).toContain('AmneziaWG');
    w.unmount();
  });
});
