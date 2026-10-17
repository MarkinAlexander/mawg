import { beforeEach, describe, expect, it, vi } from 'vitest'
import { PRIVACY_LS_KEY } from './privacy'

// стор модульный с ленивым чтением localStorage (один раз на первое
// usePrivacy) - каждый тест берёт свежий экземпляр модуля через resetModules
async function fresh() {
  vi.resetModules();
  return import('./privacy');
}

describe('privacy store', () => {
  beforeEach(() => {
    localStorage.removeItem(PRIVACY_LS_KEY);
  });

  it('по умолчанию выключен (ключа нет)', async () => {
    const { usePrivacy } = await fresh();
    expect(usePrivacy().state.on).toBe(false);
  });

  it('восстанавливается из localStorage (mawg-privacy=1)', async () => {
    localStorage.setItem(PRIVACY_LS_KEY, '1');
    const { usePrivacy } = await fresh();
    expect(usePrivacy().state.on).toBe(true);
  });

  it('значение не «1» читается как выключено', async () => {
    localStorage.setItem(PRIVACY_LS_KEY, 'yes');
    const { usePrivacy } = await fresh();
    expect(usePrivacy().state.on).toBe(false);
  });

  it('set/toggle пишут стор и localStorage', async () => {
    const { usePrivacy } = await fresh();
    const { state, set, toggle } = usePrivacy();
    set(true);
    expect(state.on).toBe(true);
    expect(localStorage.getItem(PRIVACY_LS_KEY)).toBe('1');
    toggle();
    expect(state.on).toBe(false);
    expect(localStorage.getItem(PRIVACY_LS_KEY)).toBe('0');
    toggle();
    expect(state.on).toBe(true);
    expect(localStorage.getItem(PRIVACY_LS_KEY)).toBe('1');
  });
});
