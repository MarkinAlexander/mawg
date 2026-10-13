import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { startUpdateWait } from './updateFlow'

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

function okResp(version?: string) {
  return { status: 200, ok: true, json: async () => ({ version }) };
}
function resp401() {
  return { status: 401, ok: false, json: async () => ({}) };
}

describe('updateFlow (ожидание возврата панели после обновления)', () => {
  it('версия сменилась -> reload, диалог не переходит в failed', async () => {
    const onReload = vi.fn();
    const fetchFn = vi.fn(async () => okResp('v2'));
    const w = startUpdateWait('v1', 'v2', { onReload, fetchFn });
    expect(w.state.text).toContain('скачиваем и устанавливаем v2');
    await vi.advanceTimersByTimeAsync(3000); // стартовая пауза + первый poll
    expect(fetchFn).toHaveBeenCalledTimes(1);
    expect(onReload).toHaveBeenCalledTimes(1);
    expect(w.state.phase).toBe('wait'); // reload - не ошибка
  });

  it('401 = панель уже перезапустилась -> reload (легаси-семантика)', async () => {
    const onReload = vi.fn();
    startUpdateWait('v1', 'v2', { onReload, fetchFn: async () => resp401() });
    await vi.advanceTimersByTimeAsync(3000);
    expect(onReload).toHaveBeenCalledTimes(1);
  });

  it('версия та же -> текст «устанавливаем…», при исчерпании попыток - failed + таймаут-текст', async () => {
    const onReload = vi.fn();
    const fetchFn = vi.fn(async () => okResp('v1')); // версия не меняется
    const w = startUpdateWait('v1', 'v2', { onReload, fetchFn, tries: 1 });
    await vi.advanceTimersByTimeAsync(3000);
    expect(w.state.text).toBe('устанавливаем v2 (панель ещё работает на v1)...');
    expect(w.state.phase).toBe('wait');
    await vi.advanceTimersByTimeAsync(2000); // вторая попытка (n=0) -> таймаут
    expect(w.state.phase).toBe('failed');
    expect(w.state.text).toContain('Не дождались возврата панели за 5 минут');
    expect(onReload).not.toHaveBeenCalled();
  });

  it('сетевые сбои -> «панель перезапускается…», исчерпание -> failed', async () => {
    const fetchFn = vi.fn(async () => { throw new TypeError('down'); });
    const w = startUpdateWait('v1', 'v2', { fetchFn, onReload: vi.fn(), tries: 1 });
    await vi.advanceTimersByTimeAsync(3000);
    expect(w.state.text).toBe('панель перезапускается, ждём возврата...');
    expect(w.state.phase).toBe('wait');
    await vi.advanceTimersByTimeAsync(2000); // вторая попытка (n=0) -> таймаут
    expect(w.state.phase).toBe('failed');
    expect(w.state.text).toContain('Не дождались возврата панели');
  });

  it('секундомер тикает раз в секунду; stop() гасит таймеры', async () => {
    const fetchFn = vi.fn(async () => okResp('v1'));
    const w = startUpdateWait('v1', 'v2', { fetchFn, onReload: vi.fn(), tries: 5 });
    await vi.advanceTimersByTimeAsync(2000);
    expect(w.state.seconds).toBe(2);
    w.stop();
    const calls = fetchFn.mock.calls.length;
    await vi.advanceTimersByTimeAsync(60000);
    expect(w.state.seconds).toBe(2); // секундомер остановлен
    expect(fetchFn.mock.calls.length).toBe(calls); // поллинг не стартовал
  });
});
