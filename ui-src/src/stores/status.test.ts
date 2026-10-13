import { beforeEach, describe, expect, it, vi } from 'vitest'
import { UNAUTHORIZED_EVENT } from '../api/client'
import { useStatus } from './status'

// стаб fetch по пути: {'/status': {...}, '/magitrickle': {...}}
function apiStub(routes: Record<string, unknown>) {
  return vi.fn(async (url: string) => {
    const key = url.replace('/api/v1', '');
    const body = routes[key] !== undefined ? routes[key] : {};
    return new Response(JSON.stringify(body), { status: 200 });
  });
}

beforeEach(() => {
  vi.unstubAllGlobals();
  // сброс синглтон-стора между тестами
  const { state } = useStatus();
  state.loaded = false;
  state.authNeeded = false;
  state.error = '';
  state.mt = 'checking';
  state.data = null;
});

describe('status store', () => {
  it('refresh наполняет статус и сбрасывает ошибку', async () => {
    vi.stubGlobal('fetch', apiStub({
      '/status': { version: 'v0.6.10', platform: 'keenetic', pools: [] },
    }));
    const { state, refresh } = useStatus();
    state.error = 'старая';
    await refresh();
    expect(state.loaded).toBe(true);
    expect(state.data?.version).toBe('v0.6.10');
    expect(state.error).toBe('');
  });

  it('ошибка сети сохраняет прошлые данные и пишет error (легаси: #meta = «ошибка: …»)', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => {
      throw new TypeError('fail');
    }));
    const { state, refresh } = useStatus();
    await refresh();
    expect(state.data).toBeNull();
    expect(state.error).toBe('панель недоступна');
    // повторный успешный опрос ошибку снимает
    vi.stubGlobal('fetch', apiStub({ '/status': { version: 'v1', platform: 'p', pools: [] } }));
    await refresh();
    expect(state.error).toBe('');
    expect(state.data?.version).toBe('v1');
  });

  it('refreshMT: доступен / недоступен / сетевой сбой = недоступен', async () => {
    const { state, refreshMT } = useStatus();
    vi.stubGlobal('fetch', apiStub({ '/magitrickle': { available: true, groups: [] } }));
    await refreshMT();
    expect(state.mt).toBe('yes');
    vi.stubGlobal('fetch', apiStub({ '/magitrickle': { available: false } }));
    await refreshMT();
    expect(state.mt).toBe('no');
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('x'); }));
    await refreshMT();
    expect(state.mt).toBe('no');
  });

  it('start тикает /status каждые 5с и /magitrickle каждые 30с, stop гасит оба', async () => {
    vi.useFakeTimers();
    try {
      const f = apiStub({ '/status': { version: 'v1', platform: 'p', pools: [] }, '/magitrickle': { available: true } });
      vi.stubGlobal('fetch', f);
      const { start, stop } = useStatus();
      start();
      await vi.advanceTimersByTimeAsync(0); // немедленные первые опросы
      const calls = () => f.mock.calls.map((c) => c[0] as string);
      expect(calls().filter((u) => u.endsWith('/status'))).toHaveLength(1);
      expect(calls().filter((u) => u.endsWith('/magitrickle'))).toHaveLength(1);
      await vi.advanceTimersByTimeAsync(5000); // t=5с
      expect(calls().filter((u) => u.endsWith('/status'))).toHaveLength(2);
      expect(calls().filter((u) => u.endsWith('/magitrickle'))).toHaveLength(1); // ещё только 5с
      await vi.advanceTimersByTimeAsync(25000); // суммарно 30с: тики статуса на 10..30с
      expect(calls().filter((u) => u.endsWith('/status'))).toHaveLength(7);
      expect(calls().filter((u) => u.endsWith('/magitrickle'))).toHaveLength(2);
      stop();
      await vi.advanceTimersByTimeAsync(60000);
      expect(calls().filter((u) => u.endsWith('/status'))).toHaveLength(7);
      expect(calls().filter((u) => u.endsWith('/magitrickle'))).toHaveLength(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it('UNAUTHORIZED_EVENT -> authNeeded (слушатель живёт, пока store запущен)', () => {
    vi.stubGlobal('fetch', apiStub({ '/status': { version: 'v1', platform: 'p', pools: [] } }));
    const { state, start, stop } = useStatus();
    state.loaded = true;
    start();
    stop();
    // слушатель снимается stop() - событие игнорируется
    window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT));
    expect(state.authNeeded).toBe(false);
    start();
    window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT));
    expect(state.authNeeded).toBe(true);
    expect(state.loaded).toBe(false);
    stop();
  });
});
