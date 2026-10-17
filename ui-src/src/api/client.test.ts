import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, UNAUTHORIZED_EVENT, api } from './client'

function fetchOnce(status: number, body: string | object) {
  const text = typeof body === 'string' ? body : JSON.stringify(body);
  const resp = new Response(text, { status, headers: { 'Content-Type': 'application/json' } });
  vi.stubGlobal('fetch', vi.fn(async () => resp));
}

beforeEach(() => vi.unstubAllGlobals());
afterEach(() => vi.unstubAllGlobals());

describe('api client', () => {
  it('успех: JSON парсится, путь ведёт на /api/v1', async () => {
    const fetchMock = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ ok: 1 }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const r = await api<{ ok: number }>('GET', '/status');
    expect(r.ok).toBe(1);
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/status');
  });

  it('ошибка {error} -> ApiError с текстом бэкенда', async () => {
    fetchOnce(400, { error: 'нужны name и interface' });
    await expect(api('POST', '/x', {})).rejects.toMatchObject({
      status: 400,
      message: 'нужны name и interface',
    });
  });

  it('ошибка без JSON -> HTTP-код', async () => {
    fetchOnce(500, 'oops');
    await expect(api('GET', '/x')).rejects.toMatchObject({ status: 500, message: 'HTTP 500' });
  });

  it('401: событие + заморозка вызова (страницу перезагрузит оверлей)', async () => {
    fetchOnce(401, { error: 'unauthorized' });
    let fired = '';
    window.addEventListener(UNAUTHORIZED_EVENT, (e) => {
      fired = (e as CustomEvent).detail as string;
    });
    const result = await Promise.race([
      api('GET', '/mt/groups').then(() => 'resolved'),
      new Promise((r) => setTimeout(() => r('pending'), 50)),
    ]);
    expect(fired).toBe('/mt/groups');
    expect(result).toBe('pending'); // вызов не разрешается никогда
  });

  it('401 на /auth/* не замораживает - ошибкой', async () => {
    fetchOnce(401, { error: 'не верный пароль' });
    await expect(api('POST', '/auth/login', { p: 1 })).rejects.toBeInstanceOf(ApiError);
  });

  it('таймаут -> ApiError «таймаут запроса»', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', vi.fn((_url, init?: RequestInit) => new Promise((_res, rej) => {
      init?.signal?.addEventListener('abort', () => rej(new DOMException('aborted', 'AbortError')));
    })));
    const p = api('GET', '/slow', undefined, { timeoutMs: 1000 });
    const assertion = expect(p).rejects.toMatchObject({ message: 'таймаут запроса' });
    await vi.advanceTimersByTimeAsync(1100);
    await assertion;
    vi.useRealTimers();
  });

  it('пустое тело -> undefined', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 200 })));
    expect(await api('POST', '/x', {})).toBeUndefined();
  });
});
