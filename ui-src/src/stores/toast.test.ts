import { beforeEach, describe, expect, it, vi } from 'vitest'
import { dismissToast, useToast } from './toast'

describe('toast store', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    for (const id of [...useToast().toasts.list.map((t) => t.id)]) dismissToast(id);
  });
  vi.useRealTimers();

  it('push добавляет, автоскрытие через 6с', () => {
    vi.useFakeTimers();
    const { toasts, push } = useToast();
    push('сохранено');
    expect(toasts.list).toHaveLength(1);
    expect(toasts.list[0]).toMatchObject({ text: 'сохранено', err: false });
    vi.advanceTimersByTime(6100);
    expect(toasts.list).toHaveLength(0);
  });

  it('ошибочный тост помечен err', () => {
    vi.useFakeTimers();
    const { toasts, push } = useToast();
    push('упало', true);
    expect(toasts.list[0].err).toBe(true);
  });

  it('не больше 5 на экране - старейший уходит первым', () => {
    vi.useFakeTimers();
    const { toasts, push } = useToast();
    for (let i = 0; i < 7; i++) push('тост ' + i);
    expect(toasts.list).toHaveLength(5);
    expect(toasts.list[0].text).toBe('тост 2');
    expect(toasts.list[4].text).toBe('тост 6');
  });
});
