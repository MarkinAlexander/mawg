import { describe, expect, it } from 'vitest'
import { useConfirm } from './confirm'

describe('confirm store', () => {
  it('confirm открывает модалку, ok resolves true', async () => {
    const { state, confirm, ok } = useConfirm();
    const p = confirm({ title: 'Удалить?', text: 'точно?', okLabel: 'Удалить' });
    expect(state.open).toBe(true);
    expect(state.title).toBe('Удалить?');
    expect(state.okLabel).toBe('Удалить');
    ok();
    await expect(p).resolves.toBe(true);
    expect(state.open).toBe(false);
  });

  it('cancel resolves false, state закрывается', async () => {
    const { state, confirm, cancel } = useConfirm();
    const p = confirm({ title: 't', text: 'x' });
    cancel();
    await expect(p).resolves.toBe(false);
    expect(state.open).toBe(false);
  });

  it('повторный confirm отменяет предыдущий как false', async () => {
    const { state, confirm, ok } = useConfirm();
    const p1 = confirm({ title: 'первый', text: '' });
    const p2 = confirm({ title: 'второй', text: '' });
    await expect(p1).resolves.toBe(false);
    expect(state.title).toBe('второй');
    ok();
    await expect(p2).resolves.toBe(true);
  });
});
