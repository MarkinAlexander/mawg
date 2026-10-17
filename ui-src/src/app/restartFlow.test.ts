import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { startRestartWait } from './restartFlow'

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

const ok = { ok: true, status: 200 };
const unauthorized = { ok: false, status: 401 };
const bad = { ok: false, status: 502 };

describe('restartFlow (ожидание возврата панели после /system/restart)', () => {
  it('панель вернулась (ok) -> onDone(true)', async () => {
    const onDone = vi.fn();
    const fetchFn = vi.fn(async () => ok);
    const w = startRestartWait(null, { onDone, fetchFn });
    await vi.advanceTimersByTimeAsync(4000);
    expect(fetchFn).toHaveBeenCalledWith('/api/v1/status', { credentials: 'same-origin' });
    expect(onDone).toHaveBeenCalledWith(true);
    expect(w.state.running).toBe(false);
  });

  it('401 считается возвратом (сессия сброшена рестартом)', async () => {
    const onDone = vi.fn();
    startRestartWait(null, { onDone, fetchFn: async () => unauthorized });
    await vi.advanceTimersByTimeAsync(4000);
    expect(onDone).toHaveBeenCalledWith(true);
  });

  it('пока отвечает bad - ретраи каждые 2с, потом успех', async () => {
    const onDone = vi.fn();
    const fetchFn = vi.fn().mockResolvedValueOnce(bad).mockResolvedValueOnce(bad).mockResolvedValue(ok);
    startRestartWait(null, { onDone, fetchFn });
    await vi.advanceTimersByTimeAsync(4000);
    expect(onDone).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(2000);
    expect(onDone).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(2000);
    expect(onDone).toHaveBeenCalledWith(true);
  });

  it('исчерпание попыток -> onDone(false)', async () => {
    const onDone = vi.fn();
    startRestartWait(null, { onDone, fetchFn: async () => bad, tries: 1 });
    await vi.advanceTimersByTimeAsync(4000);
    expect(onDone).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(2000);
    expect(onDone).toHaveBeenCalledWith(false);
  });

  it('переезд на новый origin: no-cors запрос, любой resolve = успех', async () => {
    const onDone = vi.fn();
    const fetchFn = vi.fn(async () => ({ ok: true, status: 0 })); // opaque response
    const w = startRestartWait('http://192.168.0.1:8091', { onDone, fetchFn });
    await vi.advanceTimersByTimeAsync(4000);
    expect(fetchFn).toHaveBeenCalledWith('http://192.168.0.1:8091/api/v1/status', { mode: 'no-cors' });
    expect(onDone).toHaveBeenCalledWith(true);
    expect(w.state.running).toBe(false);
  });

  it('stop() гасит ожидание без onDone', async () => {
    const onDone = vi.fn();
    const fetchFn = vi.fn(async () => bad);
    const w = startRestartWait(null, { onDone, fetchFn });
    w.stop();
    await vi.advanceTimersByTimeAsync(60000);
    expect(onDone).not.toHaveBeenCalled();
    expect(w.state.running).toBe(false);
  });
});
