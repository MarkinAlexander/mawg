import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import UpdateBadge from './UpdateBadge.vue'
import { ConfirmHost } from '../components/ui'
import { useSystem } from '../stores/system'
import { useConfirm } from '../stores/confirm'

// Бейдж обновления в шапке: виден по upd.info.update, клик - confirm
// (сторовый, рендерит ConfirmHost рядом), ОК -> POST /system/update/run
// и диалог ожидания. Реальный reload покрыт updateFlow.test.ts.

function apiStub() {
  return vi.fn(async (url: string) => {
    if (url.endsWith('/system/update/run')) return new Response(JSON.stringify({ ok: 'started' }), { status: 200 });
    return new Response(JSON.stringify({}), { status: 200 });
  });
}

beforeEach(() => {
  vi.unstubAllGlobals();
  useSystem().upd.info = null;
  useConfirm().cancel();
});

afterEach(() => {
  useSystem().upd.info = null;
});

// открытые диалоги (у закрытых UpdateBadge-прогресс/confirm DOM остаётся)
function openDialogs(w: ReturnType<typeof mount>) {
  return w.findAll('dialog').filter((d) => d.attributes('open') !== undefined);
}

describe('UpdateBadge', () => {
  it('без обновления бейдж скрыт; с обновлением - текст и титул легаси', () => {
    const w = mount(UpdateBadge);
    expect(w.find('.updbadge').exists()).toBe(false);
    const { upd } = useSystem();
    upd.info = { current: 'v0.6.10', latest: 'v0.6.11', update: true };
    return nextTick().then(() => {
      const b = w.find('.updbadge');
      expect(b.exists()).toBe(true);
      expect(b.text()).toBe('обновиться до v0.6.11');
      expect(b.attributes('title')).toContain('текущая v0.6.10');
    });
  });

  it('клик -> confirm; Отмена - POST не уходит', async () => {
    const f = apiStub();
    vi.stubGlobal('fetch', f);
    useSystem().upd.info = { current: 'v1', latest: 'v2', update: true };
    const w = mount(defineComponent(() => () => [h(UpdateBadge), h(ConfirmHost)]));
    await w.find('.updbadge').trigger('click');
    await nextTick();
    const dlg = openDialogs(w).find((d) => d.find('h3').text() === 'Обновление mawg')!;
    expect(dlg.text()).toContain('Скачать и установить v2?');
    await dlg.findAll('.btnrow button')[0].trigger('click'); // Отмена
    await nextTick();
    expect(f.mock.calls.map((c) => c[0])).not.toContain('/api/v1/system/update/run');
    w.unmount();
  });

  it('клик -> confirm ОК -> POST и диалог ожидания (спиннер, секундамер, легаси-текст)', async () => {
    vi.useFakeTimers();
    try {
      const f = apiStub();
      vi.stubGlobal('fetch', f);
      useSystem().upd.info = { current: 'v1', latest: 'v2', update: true };
      const w = mount(defineComponent(() => () => [h(UpdateBadge), h(ConfirmHost)]));
      await w.find('.updbadge').trigger('click');
      await nextTick();
      const confirmDlg = openDialogs(w).find((d) => d.find('h3').text() === 'Обновление mawg')!;
      await confirmDlg.findAll('.btnrow button')[1].trigger('click'); // ОК = «Обновить»
      await vi.advanceTimersByTimeAsync(0);
      // POST ушёл, диалог ожидания открыт с легаси-текстом и спиннером
      expect(f.mock.calls.map((c) => c[0])).toContain('/api/v1/system/update/run');
      const dlg = openDialogs(w)[0];
      expect(dlg.text()).toContain('скачиваем и устанавливаем v2');
      expect(dlg.find('.uwait-spin').exists()).toBe(true);
      expect(dlg.text()).toMatch(/\d+ с/);
      // «Закрыть» в фазе ожидания нет (легаси: закрытие запрещено)
      expect(dlg.findAll('button').map((b) => b.text())).not.toContain('Закрыть');
      w.unmount();
    } finally {
      vi.useRealTimers();
    }
  });
});
