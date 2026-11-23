import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useBundles } from './bundles'

function stub(routes: Record<string, unknown>) {
  return vi.fn(async (url: string, init?: { method?: string; body?: string }) => {
    const key = Object.keys(routes).find((k) => (url as string).endsWith(k));
    const v = key ? routes[key] : {};
    const body = typeof v === 'function' ? (v as (i?: typeof init) => unknown)(init) : v;
    return new Response(JSON.stringify(body), { status: 200 });
  });
}

const bundle = {
  name: 'main', members: ['proton', 'nwg7'], groups: ['g1', 'g2'],
  currentMember: 'proton', memberDevices: { proton: 'nwg0', nwg7: 'nwg7' },
};

beforeEach(() => {
  vi.unstubAllGlobals();
  const { state } = useBundles();
  state.loaded = false;
  state.list = [];
  state.rules = null;
  useBundles().stop();
});

describe('bundles store', () => {
  it('rules: грузятся с /mt/bundle-rules, сохраняются PUT-ом туда же', async () => {
    const f = stub({ '/mt/bundle-rules': { healthSource: 'probe', failCycles: 2, restoreCycles: 3 } });
    vi.stubGlobal('fetch', f);
    const { state, loadRules, saveRules } = useBundles();
    await loadRules();
    expect(state.rules).toEqual({ healthSource: 'probe', failCycles: 2, restoreCycles: 3 });

    await saveRules({ healthSource: 'state', failCycles: 4, restoreCycles: 3 });
    const put = f.mock.calls.find((c) => c[1]?.method === 'PUT')!;
    expect(put[0]).toMatch(/\/mt\/bundle-rules$/);
    expect(JSON.parse(put[1]!.body as string)).toEqual({ healthSource: 'state', failCycles: 4, restoreCycles: 3 });
  });
  it('refresh наполняет список; сетевой сбой молча держит прошлые', async () => {
    vi.stubGlobal('fetch', stub({ '/bundles': { bundles: [bundle] } }));
    const { state, refresh } = useBundles();
    await refresh();
    expect(state.loaded).toBe(true);
    expect(state.list).toHaveLength(1);
    expect(state.list[0].name).toBe('main');

    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('fail'); }));
    await refresh();
    expect(state.list).toHaveLength(1); // легаси: console.warn + держим
  });

  it('save: POST при создании, PUT /bundles/{name} при правке; после - перечитывание', async () => {
    const f = stub({ '/bundles': { bundles: [bundle] } });
    vi.stubGlobal('fetch', f);
    const { save } = useBundles();
    const body = { name: 'new1', members: ['proton'], groups: ['g1'] };
    await save(null, body);
    await save('main', body);
    const posts = f.mock.calls.filter((c) => (c[0] as string).endsWith('/bundles') && c[1]?.method === 'POST');
    expect(JSON.parse(posts[0][1]!.body as string)).toEqual(body);
    const puts = f.mock.calls.filter((c) => (c[0] as string).endsWith('/bundles/main') && c[1]?.method === 'PUT');
    expect(JSON.parse(puts[0][1]!.body as string)).toEqual(body);
    // 2 перечитывания после мутаций + 0 стартовых
    expect(f.mock.calls.filter((c) => (c[0] as string).endsWith('/bundles') && (!c[1]?.method || c[1].method === 'GET'))).toHaveLength(2);
  });

  it('del: DELETE + перечитывание', async () => {
    const f = stub({ '/bundles': { bundles: [] } });
    vi.stubGlobal('fetch', f);
    await useBundles().del('main');
    const call = f.mock.calls.find((c) => (c[0] as string).endsWith('/bundles/main'));
    expect(call![1]!.method).toBe('DELETE');
    expect(useBundles().state.list).toHaveLength(0);
  });

  it('start/stop: опрос 30с, stop гасит таймер', async () => {
    vi.useFakeTimers();
    try {
      const f = stub({ '/bundles': { bundles: [] } });
      vi.stubGlobal('fetch', f);
      const { start, stop } = useBundles();
      start(30000);
      await vi.advanceTimersByTimeAsync(0);
      const gets = () => f.mock.calls.filter((c) => (c[0] as string).endsWith('/bundles')).length;
      expect(gets()).toBe(1);
      await vi.advanceTimersByTimeAsync(30000);
      expect(gets()).toBe(2);
      stop();
      await vi.advanceTimersByTimeAsync(60000);
      expect(gets()).toBe(2);
    } finally {
      vi.useRealTimers();
    }
  });
});
