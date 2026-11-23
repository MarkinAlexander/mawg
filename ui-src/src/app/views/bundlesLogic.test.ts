import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { HEALTH_SOURCE_OPTIONS, memberBadge, useBundleRulesForm } from './bundlesLogic'
import { useBundles } from '../../stores/bundles'
import { useToast } from '../../stores/toast'
import type { BundleView } from '../../api/types'

const bundle: BundleView = {
  name: 'awg-pool', members: ['ferret', 'spare'], groups: ['g1'],
  currentMember: 'nwg5', memberDevices: { ferret: 'nwg7', spare: 'nwg5' },
  memberHealth: {
    ferret: { device: 'nwg7', ok: false, note: 'ждёт подтверждения пробой' },
    spare: { device: 'nwg5', ok: true, note: 'ok (handshake 3s ago, icmp 45ms)' },
  },
};

function stub(rules: unknown) {
  return vi.fn(async (_url: string, init?: { method?: string; body?: string }) =>
    new Response(JSON.stringify(init?.method === 'PUT' ? JSON.parse(init.body as string) : rules), { status: 200 }));
}

beforeEach(() => {
  vi.unstubAllGlobals();
  useBundles().stop();
  useBundles().state.rules = null;
  useToast().toasts.list.splice(0);
});

describe('memberBadge', () => {
  it('мёртвый запасной - красный с причиной, живой текущий - зелёный', () => {
    expect(memberBadge(bundle, 'ferret')).toEqual({ variant: 'err', tip: 'ferret - не жив: ждёт подтверждения пробой' });
    expect(memberBadge(bundle, 'spare')).toEqual({ variant: 'ok', tip: 'spare - жив: ok (handshake 3s ago, icmp 45ms)' });
  });

  it('текущий, который сам не жив, - жёлтый; без данных о здоровье - как раньше', () => {
    const held = { ...bundle, currentMember: 'nwg7' };
    expect(memberBadge(held, 'ferret').variant).toBe('warn');
    const blind: BundleView = { ...bundle, memberHealth: undefined };
    expect(memberBadge(blind, 'ferret')).toEqual({ variant: 'muted', tip: 'ferret' });
  });

  it('пока проверки не было - нейтральный, а не «мёртвый»', () => {
    const waiting: BundleView = { ...bundle, memberHealth: { ferret: { device: 'nwg7', ok: false, note: 'проверяется', pending: true } } };
    expect(memberBadge(waiting, 'ferret')).toEqual({ variant: 'muted', tip: 'ferret - проверяется' });
  });
});

describe('useBundleRulesForm', () => {
  it('форма заполняется из правил, сохранение шлёт их обратно и гаснет', async () => {
    const f = stub({ healthSource: 'state', failCycles: 2, restoreCycles: 3 });
    vi.stubGlobal('fetch', f);
    const form = useBundleRulesForm();
    await useBundles().loadRules();
    await nextTick();
    expect(form.source.value).toBe('state');
    expect(form.fail.value).toBe('2');
    expect(form.changed.value).toBe(false);

    form.restore.value = '10';
    form.source.value = HEALTH_SOURCE_OPTIONS[0].value;
    await nextTick();
    expect(form.changed.value).toBe(true);

    await form.save();
    const put = f.mock.calls.find((c) => c[1]?.method === 'PUT')!;
    expect(JSON.parse(put[1]!.body as string)).toEqual({ healthSource: 'probe', failCycles: 2, restoreCycles: 10 });
    expect(useToast().toasts.list.map((t) => t.text)).toContain('Наборы: участник живой по ответу на пробу, уход после 2 проверок без ответа, возврат после 10 с ответом');
    expect(form.changed.value).toBe(false);
  });

  it('кривые циклы не дают сохранить; ошибка сервера - тост', async () => {
    vi.stubGlobal('fetch', stub({ healthSource: 'probe', failCycles: 2, restoreCycles: 3 }));
    const form = useBundleRulesForm();
    await useBundles().loadRules();
    form.fail.value = '0';
    await nextTick();
    expect(form.changed.value).toBe(false);
    await form.save();

    vi.stubGlobal('fetch', vi.fn(async () => new Response('{"error":"циклы должны быть от 1 до 20"}', { status: 400 })));
    form.fail.value = '5';
    await nextTick();
    await form.save();
    expect(useToast().toasts.list.map((t) => t.text)).toContain('циклы должны быть от 1 до 20');
  });
});
