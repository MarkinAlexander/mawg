import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useServerSettings } from './settings'
import type { ServerSettings } from '../api/types'

const full: ServerSettings = {
  listenAddr: '0.0.0.0',
  port: 8090,
  allowedIps: [{ v: '192.168.0.0/24', on: true }],
  authDisabled: false,
  authEnabled: true,
  hasCreds: true,
  clientIp: '192.168.0.82',
};

beforeEach(() => {
  vi.unstubAllGlobals();
  const { state } = useServerSettings();
  state.loaded = false;
  state.data = null;
});

describe('settings store', () => {
  it('load наполняет состояние', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(full), { status: 200 })));
    const { state, load } = useServerSettings();
    const d = await load();
    expect(d?.port).toBe(8090);
    expect(state.loaded).toBe(true);
    expect(state.data?.allowedIps).toHaveLength(1);
  });

  it('saveAllowed шлёт только allowedIps и применяет эффективный список ответа', async () => {
    const f = vi.fn(async (_url: string, init?: { body?: string }) => {
      const sent = JSON.parse(init?.body || '{}');
      // бэкенд дописал адрес клиента, которого клиент не слал
      return new Response(
        JSON.stringify({ ok: true, allowedIps: [...sent.allowedIps, { v: '192.168.0.82', on: true }] }),
        { status: 200 },
      );
    });
    vi.stubGlobal('fetch', f);
    const { saveAllowed } = useServerSettings();
    const effective = await saveAllowed([{ v: '192.168.0.0/24', on: true }]);
    const sent = JSON.parse(f.mock.calls[0][1]?.body as string);
    expect(Object.keys(sent)).toEqual(['allowedIps']); // сеть не задевается
    expect(effective).toHaveLength(2); // вернулся эффективный список
  });

  it('save передаёт все поля, возвращает needRestart и перезагружает', async () => {
    const after = { ...full, listenAddr: '127.0.0.1', port: 8091 };
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init?: { body?: string }) => {
      if (init?.body) {
        const sent = JSON.parse(init.body);
        expect(sent).toMatchObject({ listenAddr: '127.0.0.1', port: 8091, authDisabled: false });
        return new Response(JSON.stringify({ ok: true, needRestart: true }), { status: 200 });
      }
      return new Response(JSON.stringify(after), { status: 200 });
    }));
    const { save, state } = useServerSettings();
    const r = await save({ listenAddr: '127.0.0.1', port: 8091, authDisabled: false });
    expect(r.needRestart).toBe(true);
    expect(state.data?.port).toBe(8091); // перезагружено после save
  });
});
