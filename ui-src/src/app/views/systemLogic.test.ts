import { describe, expect, it } from 'vitest'
import { engineInfoOf, engineUsable } from './systemLogic'
import type { EngineInfo } from '../../api/types'

// слово владельца 24.11: ядро установлено, но не отвечает на запрос версии
// (на роутере без памяти version-зонд убивает OOM) - оно не «отсутствует»,
// но и движком пула его не предлагают
describe('systemLogic - установленное, но глухое ядро', () => {
  const deaf: EngineInfo = { available: true, ready: false, running: false };

  it('engineUsable: годно только установленное и отвечающее', () => {
    expect(engineUsable(undefined)).toBe(false);
    expect(engineUsable({ available: false })).toBe(false);
    expect(engineUsable({ available: true })).toBe(true);
    expect(engineUsable({ available: true, running: true })).toBe(true);
    expect(engineUsable(deaf)).toBe(false);
  });

  it('engineInfoOf: честное состояние вместо «движок не найден»', () => {
    expect(engineInfoOf(deaf)).toBe('ядро не отвечает на запрос версии (мало памяти?)');
    expect(engineInfoOf(undefined)).toBe('движок не найден');
    expect(engineInfoOf({ available: false })).toBe('движок не найден');
    expect(engineInfoOf({ available: true })).toBe('ядро не запущено');
    expect(engineInfoOf({ available: true, running: true, api: false })).toBe('ядро запущено, но не отвечает');
    expect(engineInfoOf({ available: true, running: true })).toBe('ядро работает');
  });
});
