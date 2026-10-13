import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import SettingsView from './SettingsView.vue'
import { ConfirmHost } from '../../components/ui'
import { useServerSettings } from '../../stores/settings'
import { useStatus } from '../../stores/status'
import { useSystem } from '../../stores/system'
import { useConfirm } from '../../stores/confirm'
import { dismissToast, useToast } from '../../stores/toast'
import type { ServerSettings, Status } from '../../api/types'

const srvData: ServerSettings = {
  listenAddr: '0.0.0.0',
  port: 8090,
  allowedIps: [
    { v: '192.168.0.0/24', on: true },
    { v: '192.168.0.90', on: true },
  ],
  authDisabled: false,
  authEnabled: true,
  hasCreds: true,
  clientIp: '192.168.0.82',
};

// стаб по методу и пути: GET/PUT /settings/server, /lan/clients, /auth/password
function apiStub(opts: {
  srv?: Partial<ServerSettings>
  putNeedRestart?: boolean
  putAllowedIps?: Array<{ v: string; on: boolean }>
  pwError?: string
} = {}) {
  return vi.fn(async (url: string, init?: { method?: string; body?: string }) => {
    const method = init?.method || 'GET';
    if (url.endsWith('/settings/server')) {
      if (method === 'PUT') {
        const sent = JSON.parse(init?.body || '{}');
        return new Response(
          JSON.stringify({ ok: true, needRestart: !!opts.putNeedRestart, allowedIps: opts.putAllowedIps ?? sent.allowedIps }),
          { status: 200 },
        );
      }
      return new Response(JSON.stringify({ ...srvData, ...opts.srv }), { status: 200 });
    }
    if (url.endsWith('/lan/clients')) {
      return new Response(JSON.stringify({ clients: [{ ip: '192.168.0.50', name: 'tv' }] }), { status: 200 });
    }
    if (url.endsWith('/auth/password')) {
      if (opts.pwError) return new Response(JSON.stringify({ error: opts.pwError }), { status: 400 });
      return new Response(JSON.stringify({ ok: true }), { status: 200 });
    }
    if (url.endsWith('/system/update/check')) {
      return new Response(JSON.stringify({ current: 'v1', latest: 'v2', update: false }), { status: 200 });
    }
    return new Response(JSON.stringify({}), { status: 200 });
  });
}

async function makeView(opts?: Parameters<typeof apiStub>[0]) {
  vi.stubGlobal('fetch', apiStub(opts));
  const w = mount(defineComponent(() => () => [h(SettingsView), h(ConfirmHost)]));
  await vi.waitFor(() => expect(useServerSettings().state.loaded).toBe(true));
  await nextTick();
  return w;
}

const buttonByText = (w: ReturnType<typeof mount>, text: string) =>
  w.findAll('button').filter((b) => b.text() === text);

beforeEach(() => {
  vi.unstubAllGlobals();
  const s = useServerSettings().state;
  s.loaded = false;
  s.data = null;
  useStatus().state.data = {
    version: 'v1',
    platform: 'keenetic',
    backendName: 'keenetic',
    authEnabled: true,
    nativeAWG3: false,
    mtSources: true,
    pools: [],
  } as Status;
  useSystem().upd.info = null;
  useConfirm().cancel();
  for (const t of [...useToast().toasts.list]) dismissToast(t.id);
});

describe('SettingsView - карточка настроек', () => {
  it('загрузка наполняет поля, чипы, RCI-строку keenetic; «Сохранить» зажата (не dirty)', async () => {
    const w = await makeView();
    // UiCard разрешён как компонент, а не <uicard>-кастомный элемент
    // (GOTCHAS: vue-tsc забытый импорт не ловит)
    expect(w.find('.card').exists()).toBe(true);
    expect(w.find('input[placeholder="0.0.0.0"]').element as HTMLInputElement).toBeTruthy();
    expect((w.find('input[type=number]').element as HTMLInputElement).value).toBe('8090');
    expect(w.findAll('.chip')).toHaveLength(2);
    expect(w.find('.chip').text()).toContain('192.168.0.0/24');
    expect(w.text()).toContain('Токен локального API Keenetic');
    const save = buttonByText(w, 'сохранить')[0];
    expect(save.attributes('disabled')).toBeDefined();
    w.unmount();
  });

  it('на openwrt RCI-строки нет', async () => {
    useStatus().state.data = { ...(useStatus().state.data as Status), platform: 'openwrt' };
    const w = await makeView();
    expect(w.text()).not.toContain('Токен локального API Keenetic');
    w.unmount();
  });

  it('изменение порта включает «Сохранить», PUT уходит с новыми полями, тост «Сохранено»', async () => {
    const w = await makeView();
    await w.find('input[type=number]').setValue('8091');
    const save = buttonByText(w, 'сохранить')[0];
    expect(save.attributes('disabled')).toBeUndefined();
    await save.trigger('click');
    await vi.waitFor(() => expect(useToast().toasts.list.map((t) => t.text)).toContain('Сохранено'));
    const put = (fetch as ReturnType<typeof vi.fn>).mock.calls.find(
      (c) => (c[0] as string).endsWith('/settings/server') && c[1]?.method === 'PUT',
    );
    expect(JSON.parse(put![1]!.body)).toMatchObject({ port: 8091, listenAddr: '0.0.0.0', authDisabled: false });
    w.unmount();
  });

  it('гвард авторизации: включение без сохранённой учётки откатывается с тостом', async () => {
    const w = await makeView({ srv: { hasCreds: false, authEnabled: false } });
    const sw = w.findAllComponents({ name: 'UiSwitch' })[0];
    sw.vm.$emit('update:modelValue', true);
    await nextTick();
    expect(useToast().toasts.list.some((t) => t.text.includes('Нет сохранённой учётки'))).toBe(true);
    // авторизация осталась выключенной
    const sw2 = w.findAllComponents({ name: 'UiSwitch' })[0];
    expect(sw2.props('modelValue')).toBe(false);
    w.unmount();
  });

  it('смена пароля: валидации в диалоге, POST с полями, ошибка бэкенда показывается', async () => {
    const w = await makeView({ pwError: 'неверный старый пароль' });
    await buttonByText(w, 'сменить пароль')[0].trigger('click');
    const dlg = w.findAll('dialog').find((d) => d.find('h3').text() === 'Смена пароля')!;
    const inputs = dlg.findAll('input');
    await inputs[1].setValue('short'); // новый пароль < 8
    await buttonByText(w, 'Сохранить')[0].trigger('click');
    expect(dlg.text()).toContain('минимум 8 символов');
    await inputs[1].setValue('longenough1');
    await inputs[2].setValue('longenough2');
    await buttonByText(w, 'Сохранить')[0].trigger('click');
    expect(dlg.text()).toContain('пароли не совпадают');
    await inputs[2].setValue('longenough1');
    await buttonByText(w, 'Сохранить')[0].trigger('click');
    const post = (fetch as ReturnType<typeof vi.fn>).mock.calls.find((c) => (c[0] as string).endsWith('/auth/password'));
    expect(JSON.parse(post![1]!.body)).toEqual({ old: '', password: 'longenough1' });
    await vi.waitFor(() => expect(dlg.text()).toContain('неверный старый пароль'));
    w.unmount();
  });

  it('allowAdd: мусор - тост без PUT; дубликат - тост; валидный из той же подсети - PUT и чип', async () => {
    const w = await makeView();
    const inp = w.find('input[placeholder="192.168.0.82 или 192.168.0.0/24"]');
    const puts = () => (fetch as ReturnType<typeof vi.fn>).mock.calls.filter((c) => (c[0] as string).endsWith('/settings/server') && c[1]?.method === 'PUT');

    await inp.setValue('300.1.1.1');
    await buttonByText(w, 'добавить')[0].trigger('click');
    await nextTick();
    expect(useToast().toasts.list.some((t) => t.text.includes('не похоже на IP'))).toBe(true);
    expect(puts()).toHaveLength(0);

    await inp.setValue('192.168.0.0/24');
    await buttonByText(w, 'добавить')[0].trigger('click');
    await nextTick();
    expect(useToast().toasts.list.some((t) => t.text.includes('уже есть'))).toBe(true);
    expect(puts()).toHaveLength(0);

    // 192.168.0.50 - та же подсеть (префикс совпадает с записью и clientIp) - без confirm
    await inp.setValue('192.168.0.50');
    await buttonByText(w, 'добавить')[0].trigger('click');
    await vi.waitFor(() => expect(puts()).toHaveLength(1));
    await vi.waitFor(() => expect(w.findAll('.chip')).toHaveLength(3));
    w.unmount();
  });

  it('чужая подсеть: confirm, Отмена - PUT не уходит', async () => {
    const w = await makeView();
    const inp = w.find('input[placeholder="192.168.0.82 или 192.168.0.0/24"]');
    await inp.setValue('10.0.0.5'); // подсеть 10.0 не совпадает ни с чем
    await buttonByText(w, 'добавить')[0].trigger('click');
    await nextTick();
    const dlg = w.findAll('dialog').find((d) => d.find('h3').text() === 'Другая подсеть')!;
    expect(dlg.text()).toContain('Другая подсеть');
    await dlg.findAll('.btnrow button')[0].trigger('click'); // Отмена
    await nextTick();
    const puts = (fetch as ReturnType<typeof vi.fn>).mock.calls.filter((c) => (c[0] as string).endsWith('/settings/server') && c[1]?.method === 'PUT');
    expect(puts).toHaveLength(0);
    w.unmount();
  });

  it('чекер «вся моя подсеть»: подпись с /24 из clientIp', async () => {
    const w = await makeView();
    expect(w.text()).toContain('вся моя подсеть (192.168.0.0/24)');
    w.unmount();
  });

  it('чипы - тумблеры (не чекбоксы): выключение записи уходит PUT-ом сразу', async () => {
    const w = await makeView();
    const chips = w.findAll('.chip');
    expect(chips.length).toBe(2);
    const chip = chips[1]; // 192.168.0.90
    expect(chip.find('.knob').exists()).toBe(true); // тумблер легаси-разметки
    const putsBefore = () => (fetch as ReturnType<typeof vi.fn>).mock.calls.filter((c) => (c[0] as string).endsWith('/settings/server') && c[1]?.method === 'PUT');
    expect(putsBefore()).toHaveLength(0);
    await chip.find('input[type=checkbox]').setValue(false);
    await vi.waitFor(() => expect(putsBefore()).toHaveLength(1));
    const sent = JSON.parse(putsBefore()[0][1]?.body as string);
    expect(sent.allowedIps.find((e: { v: string }) => e.v === '192.168.0.90').on).toBe(false);
    w.unmount();
  });
});
