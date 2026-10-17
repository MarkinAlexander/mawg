import { beforeEach, describe, expect, it } from 'vitest'
import { useCaptcha } from './captcha'

beforeEach(() => {
  useCaptcha().cancel(); // сброс синглтона между тестами
});

describe('captcha store', () => {
  it('ask показывает картинку и hint, answer возвращает id+решение', async () => {
    const { state, ask, answer } = useCaptcha();
    const p = ask({ id: 'cap-1', image: 'AAAA', hint: 'цифры' });
    expect(state.open).toBe(true);
    expect(state.image).toBe('AAAA');
    expect(state.hint).toBe('цифры');
    answer('1234');
    await expect(p).resolves.toEqual({ id: 'cap-1', solution: '1234' });
    expect(state.open).toBe(false);
  });

  it('без hint - дефолтный текст; cancel -> null', async () => {
    const { state, ask, cancel } = useCaptcha();
    const p = ask({ id: 'cap-2', image: 'BBB' });
    expect(state.hint).toContain('Введите цифры с картинки');
    cancel();
    await expect(p).resolves.toBeNull();
    expect(state.open).toBe(false);
  });

  it('refresh меняет картинку через refetch-колбэк; сбой держит старую', async () => {
    const { state, ask, refresh } = useCaptcha();
    const pending = ask({ id: 'cap-3', image: 'OLD' }, async () => ({ id: 'cap-4', image: 'NEW' }));
    await refresh();
    expect(state.image).toBe('NEW');
    ask({ id: 'cap-5', image: 'KEEP' }, async () => { throw new Error('сеть'); });
    await refresh();
    expect(state.image).toBe('KEEP');
    // прибираем висящие промисы
    useCaptcha().cancel();
    await pending;
    useCaptcha().cancel();
  });

  it('повторный ask отменяет предыдущий как null (легаси-семантика askModal)', async () => {
    const { ask, answer } = useCaptcha();
    const p1 = ask({ id: 'a', image: 'x' });
    const p2 = ask({ id: 'b', image: 'y' });
    await expect(p1).resolves.toBeNull();
    answer('9');
    await expect(p2).resolves.toEqual({ id: 'b', solution: '9' });
  });
});
