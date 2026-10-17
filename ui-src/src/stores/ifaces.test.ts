import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useIfaces } from './ifaces'
import type { IfaceInfo } from '../api/types'

// стаб fetch по пути: {'/ifaces': {...}, '/wanprobe': {...}}
function apiStub(routes: Record<string, unknown>) {
  return vi.fn(async (url: string, _init?: { method?: string; body?: string }) => {
    const key = url.replace('/api/v1', '');
    const body = routes[key] !== undefined ? routes[key] : {};
    return new Response(JSON.stringify(body), { status: 200 });
  });
}

const iface = (over: Partial<IfaceInfo>): IfaceInfo => ({
  device: 'nwg7', mode: 'external', linkUp: true, connected: true, handshakeAgo: 12, ...over,
});

beforeEach(() => {
  vi.unstubAllGlobals();
  const { state } = useIfaces();
  state.loaded = false;
  state.error = '';
  state.list = [];
  state.wan = { enabled: false, probe: null };
  state.wanLoaded = false;
  useIfaces().stop();
});

describe('ifaces store', () => {
  it('refresh наполняет список, ошибка без данных даёт error, с данными - держит прошлые', async () => {
    vi.stubGlobal('fetch', apiStub({ '/ifaces': { interfaces: [iface({})] } }));
    const { state, refresh } = useIfaces();
    await refresh();
    expect(state.loaded).toBe(true);
    expect(state.error).toBe('');
    expect(state.list).toHaveLength(1);
    expect(state.list[0].device).toBe('nwg7');

    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('fail'); }));
    await refresh();
    expect(state.loaded).toBe(true); // прошлые данные целы
    expect(state.error).toBe('');
    expect(state.list).toHaveLength(1);

    // без данных ошибка видна (легаси: «интерфейсы недоступны: …»)
    state.loaded = false;
    state.list = [];
    await refresh();
    expect(state.loaded).toBe(false);
    expect(state.error).toBe('панель недоступна');
  });

  it('setMode: POST {mode} + перечитывание списка', async () => {
    const f = apiStub({ '/ifaces': { interfaces: [iface({ mode: 'hidden' })] } });
    vi.stubGlobal('fetch', f);
    const { setMode } = useIfaces();
    await setMode('nwg7', 'external');
    const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/ifaces/nwg7/mode'));
    expect(post).toBeTruthy();
    expect(post![1]!.method).toBe('POST');
    expect(JSON.parse(post![1]!.body as string)).toEqual({ mode: 'external' });
    // после мутации список перечитан
    expect(useIfaces().state.list[0].mode).toBe('hidden');
  });

  it('setProbe: PUT тела сохранения и уборки {target:""}', async () => {
    const f = apiStub({ '/ifaces': { interfaces: [] } });
    vi.stubGlobal('fetch', f);
    const { setProbe } = useIfaces();
    await setProbe('nwg7', { type: 'icmp', target: '1.1.1.1', maxRttMs: 300 });
    await setProbe('nwg7', { target: '' });
    const puts = f.mock.calls.filter((c) => (c[0] as string).endsWith('/ifaces/nwg7/probe'));
    expect(puts).toHaveLength(2);
    expect(JSON.parse(puts[0][1]!.body as string)).toEqual({ type: 'icmp', target: '1.1.1.1', maxRttMs: 300 });
    expect(JSON.parse(puts[1][1]!.body as string)).toEqual({ target: '' });
  });

  it('refreshWan: включено/выключено, сетевой сбой молча держит прошлые', async () => {
    const { state, refreshWan } = useIfaces();
    vi.stubGlobal('fetch', apiStub({
      '/wanprobe': { enabled: true, probe: { type: 'http', target: 'http://x/', maxRttMs: 500 } },
    }));
    await refreshWan();
    expect(state.wan.enabled).toBe(true);
    expect(state.wan.probe).toEqual({ type: 'http', target: 'http://x/', maxRttMs: 500 });

    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('fail'); }));
    await refreshWan(); // легаси catch {} - не падает
    expect(state.wan.enabled).toBe(true);

    vi.stubGlobal('fetch', apiStub({ '/wanprobe': { enabled: false, probe: null } }));
    await refreshWan();
    expect(state.wan.enabled).toBe(false);
    expect(state.wan.probe).toBeNull();
  });

  it('saveWan: PUT {target:""} выключает, включённое - полная проба; после - перечитывание', async () => {
    const f = apiStub({
      '/wanprobe': { enabled: false, probe: null },
    });
    vi.stubGlobal('fetch', f);
    const { saveWan } = useIfaces();
    await saveWan({ type: 'http', target: 'http://www.gstatic.com/generate_204' });
    await saveWan({ target: '' });
    const puts = f.mock.calls.filter(
      (c) => (c[0] as string).endsWith('/wanprobe') && c[1]?.method === 'PUT',
    );
    expect(puts).toHaveLength(2);
    expect(JSON.parse(puts[0][1]!.body as string)).toEqual({ type: 'http', target: 'http://www.gstatic.com/generate_204' });
    expect(JSON.parse(puts[1][1]!.body as string)).toEqual({ target: '' });
    // каждое сохранение перечитывает GET (итого 2 PUT + 2 GET)
    expect(f.mock.calls.filter((c) => (c[0] as string).endsWith('/wanprobe'))).toHaveLength(4);
    expect(useIfaces().state.wan.enabled).toBe(false);
  });

  it('start/stop: опрос 30с, stop гасит таймер', async () => {
    vi.useFakeTimers();
    try {
      const f = apiStub({ '/ifaces': { interfaces: [] } });
      vi.stubGlobal('fetch', f);
      const { start, stop } = useIfaces();
      start(30000);
      await vi.advanceTimersByTimeAsync(0);
      const gets = () => f.mock.calls.filter((c) => (c[0] as string).endsWith('/ifaces')).length;
      expect(gets()).toBe(1);
      await vi.advanceTimersByTimeAsync(30000);
      expect(gets()).toBe(2);
      await vi.advanceTimersByTimeAsync(30000);
      expect(gets()).toBe(3);
      stop();
      await vi.advanceTimersByTimeAsync(60000);
      expect(gets()).toBe(3);
    } finally {
      vi.useRealTimers();
    }
  });
});
