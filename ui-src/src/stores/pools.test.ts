import { beforeEach, describe, expect, it, vi } from 'vitest'
import { usePoolActions } from './pools'
import { useStatus } from './status'

// стаб fetch: считаем вызовы по путям; FormData-тело не парсим
function apiStub() {
  return vi.fn(async (url: string, init?: { method?: string; body?: unknown }) => {
    if (init?.body instanceof FormData) {
      return new Response(JSON.stringify({ added: 2, duplicates: ['a.conf'], warnings: ['конфликт X'] }), { status: 200 });
    }
    if (typeof init?.body === 'string') return new Response(init.body, { status: 200 });
    return new Response('{"ok":true}', { status: 200 });
  });
}

beforeEach(() => {
  vi.unstubAllGlobals();
  const s = useStatus();
  s.stop();
  s.state.data = null;
  s.state.loaded = false;
});

describe('pool actions store', () => {
  it('act: POST /pools/{name}/{act} с пустым телом + отложенное перечитывание /status', async () => {
    vi.useFakeTimers();
    try {
      const f = apiStub();
      vi.stubGlobal('fetch', f);
      await usePoolActions().act('proton', 'rotate');
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/proton/rotate'));
      expect(post).toBeTruthy();
      expect(post![1]!.method).toBe('POST');
      expect(JSON.parse(post![1]!.body as string)).toEqual({});
      expect(f.mock.calls.filter((c) => (c[0] as string).endsWith('/status'))).toHaveLength(0);
      await vi.advanceTimersByTimeAsync(700);
      expect(f.mock.calls.filter((c) => (c[0] as string).endsWith('/status'))).toHaveLength(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('del: DELETE /pools/{name} + перечитывание', async () => {
    const f = apiStub();
    vi.stubGlobal('fetch', f);
    await usePoolActions().del('warp');
    const call = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/warp'));
    expect(call![1]!.method).toBe('DELETE');
    await vi.waitFor(() => expect(f.mock.calls.filter((c) => (c[0] as string).endsWith('/status'))).toHaveLength(1));
  });

  it('upload: FormData с файлами, ответ added/duplicates/warnings', async () => {
    const f = apiStub();
    vi.stubGlobal('fetch', f);
    const r = await usePoolActions().upload('proton', [new File(['a'], 'a.conf')]);
    expect(r.added).toBe(2);
    expect(r.duplicates).toEqual(['a.conf']);
    expect(r.warnings).toEqual(['конфликт X']);
    const call = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/proton/configs'));
    expect(call).toBeTruthy();
    const fd = call![1]!.body as FormData;
    expect(fd).toBeInstanceOf(FormData);
    expect(fd.get('files')).toBeTruthy();
  });

  it('toggleCfg/moveCfg/delCfg: пути и тела; пустые файлы - экранированные в URL', async () => {
    const f = apiStub();
    vi.stubGlobal('fetch', f);
    const a = usePoolActions();
    await a.toggleCfg('proton', 'wg0.conf', false);
    await a.moveCfg('proton', 'wg0.conf', -1);
    await a.delCfg('proton', 'wg 1.conf');
    const byPath = (s: string) => f.mock.calls.find((c) => (c[0] as string).endsWith(s));
    const t = byPath('/configs/wg0.conf/enable');
    expect(JSON.parse(t![1]!.body as string)).toEqual({ enabled: false });
    const m = byPath('/configs/wg0.conf/move');
    expect(JSON.parse(m![1]!.body as string)).toEqual({ delta: -1 });
    const d = byPath('/configs/wg%201.conf');
    expect(d![1]!.method).toBe('DELETE');
  });

  it('importPremium/switchPremiumCountry: тела запросов', async () => {
    const f = apiStub();
    vi.stubGlobal('fetch', f);
    const a = usePoolActions();
    await a.importPremium('prem', 'vpn://key');
    await a.switchPremiumCountry('prem', 'nl');
    const imp = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/prem/premium'));
    expect(JSON.parse(imp![1]!.body as string)).toEqual({ key: 'vpn://key' });
    const c = f.mock.calls.find((c) => (c[0] as string).endsWith('/pools/prem/premium/country'));
    expect(JSON.parse(c![1]!.body as string)).toEqual({ country: 'nl' });
  });
});
