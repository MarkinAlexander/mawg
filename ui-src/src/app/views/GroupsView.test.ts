import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import GroupsView from './GroupsView.vue'
import { ConfirmHost, UiSwitch } from '../../components/ui'
import { useConfirm } from '../../stores/confirm'
import { useGroups } from '../../stores/groups'
import { useToast } from '../../stores/toast'
import type { GroupView } from '../../api/types'

function groupsFixture(): GroupView[] {
  return [
    {
      id: 'g1', name: 'telegram', color: '#2AABEE', interface: 'nwg0', enable: true, interfaceTitle: 'пул proton',
      policy: { onDead: 'direct', sourcesMode: 'list', sources: ['192.168.1.50'], sourcesRouter: true },
      rules: [
        { id: 'r1', type: 'domain', rule: 't.me', enable: true },
        { id: 'r2', type: 'subnet', rule: '149.154.160.0/20', enable: false, name: 'tg-net' },
      ],
    },
    { id: 'g2', name: 'blackout', interface: 'blackhole', enable: false, rules: [] },
    {
      id: 'g3', name: 'каскадная', interface: 'nwg7', enable: true,
      cascade: { group: 'g3', name: 'Служебная: warp через nwg7', source: 'pool:warp', via: 'iface:nwg7' },
      degraded: 'iface:nwg7',
      rules: [{ id: 'r3', type: 'wildcard', rule: '*.example.com', enable: true }],
    },
  ];
}

type Rec = Record<string, unknown>;

async function makeView(dups: Rec = {}) {
  const data: {
    groups: GroupView[]
    dups: Rec
    cycles: { failCycles: number; restoreCycles: number }
  } = { groups: groupsFixture(), dups, cycles: { failCycles: 2, restoreCycles: 2 } };
  const ok = (x: unknown = { ok: true }) => new Response(JSON.stringify(x), { status: 200 });
  const f = vi.fn(async (url: string, init?: { method?: string; body?: string }) => {
    const u = url;
    const m = init?.method || 'GET';
    const body = init?.body ? JSON.parse(init.body) : {};
    if (u.endsWith('/mt/duplicates')) return ok({ duplicates: data.dups });
    if (u.endsWith('/mt/interfaces')) return ok({ interfaces: [{ device: 'nwg0', title: 'пул proton' }, { device: 'nwg7' }] });
    if (u.endsWith('/mt/policy-cycles')) {
      if (m === 'PUT') data.cycles = body;
      return ok(data.cycles);
    }
    // мутации меняют state-мок, следующий refresh это увидит
    if (m === 'POST' && /\/mt\/groups\/g1\/rules\/r1\/enable$/.test(u)) {
      (data.groups[0].rules![0] as unknown as Rec).enable = body.enable;
    } else if (m === 'POST' && /\/mt\/groups\/g1\/rules\/import$/.test(u)) {
      return ok({ added: 2, skipped: 1, bad: ['мусор'] });
    } else if (m === 'POST' && /\/mt\/groups\/g1\/rules$/.test(u)) {
      data.groups[0].rules!.push({ id: 'r9', type: body.type, rule: body.rule, enable: true });
      return ok({ id: 'r9', type: body.type, rule: body.rule, enable: true });
    } else if (m === 'POST' && /\/mt\/groups$/.test(u)) {
      data.groups.push({ id: 'g9', name: body.name, color: body.color, interface: body.interface, enable: true, rules: [] });
      return ok(data.groups[data.groups.length - 1]);
    } else if (m === 'PUT' && /\/mt\/groups\/g1\/rules\/r1$/.test(u)) {
      data.groups[0].rules![0] = { ...data.groups[0].rules![0], ...body };
    } else if (m === 'PUT' && /\/mt\/groups\/g1\/policy$/.test(u)) {
      data.groups[0].policy = { ...(data.groups[0].policy || {}), ...body };
    } else if (m === 'PUT' && /\/mt\/groups\/g1$/.test(u)) {
      const g = data.groups[0];
      if (body.name !== undefined) g.name = body.name;
      if (body.color !== undefined) g.color = body.color;
      if (body.interface !== undefined) g.interface = body.interface;
    } else if (m === 'DELETE' && /\/mt\/groups\/g1\?/.test(u)) {
      data.groups = data.groups.filter((g) => g.id !== 'g1');
    }
    if (u.endsWith('/mt/groups')) return ok({ available: true, glitchy: false, wrapper: false, sources: true, groups: data.groups });
    return ok();
  });
  vi.stubGlobal('fetch', f);
  const w = mount(defineComponent(() => () => [h(GroupsView), h(ConfirmHost)]));
  await vi.waitFor(() => expect(useGroups().state.loaded).toBe(true));
  await nextTick();
  return { w, f, data };
}

const block = (w: ReturnType<typeof mount>, id: string) => w.find(`[data-gid="${id}"]`);
const btnIn = (
  scope: { findAll: (s: string) => ReturnType<ReturnType<typeof mount>['findAll']> },
  text: string,
) => scope.findAll('button').filter((b) => b.text() === text);

beforeEach(() => {
  vi.unstubAllGlobals();
  const { state, stop } = useGroups();
  stop();
  state.editing = 0;
  state.pending = false;
  state.lastJSON = '';
  state.groups = [];
  state.duplicates = {};
  state.ifaces = [];
  state.cycles = null;
  state.available = true;
  state.loaded = false;
  useConfirm().cancel();
  useToast().toasts.list.splice(0);
});

describe('GroupsView - список групп', () => {
  it('компоненты разрешены (не кастом-теги), бейджи/деградация/счётчики строк', async () => {
    const { w } = await makeView({ 't.me': [{ groupName: 'ai', ruleType: 'domain' }] });
    const html = w.html();
    for (const tag of ['<uicard', '<uiswitch', '<uiselect', '<uidialog', '<uiinput', '<uibutton', '<uibanner']) {
      expect(html.includes(tag)).toBe(false);
    }
    expect(w.findComponent(UiSwitch).exists()).toBe(true);

    const g1 = block(w, 'g1');
    expect(g1.text()).toContain('telegram');
    expect(g1.text()).toContain('при отвале: прямой ход');
    expect(g1.text()).toContain('источники: IP-список (1) + роутер');
    expect(g1.text()).toContain('nwg0');
    expect(g1.text()).toContain('пул proton');
    expect(g1.text()).toContain('правил: 2');
    expect(g1.find('.warnb').exists()).toBe(true); // дубль t.me в другой группе
    expect(g1.attributes('style')).toContain('--gcol');

    expect(block(w, 'g2').text()).toContain('блокировка');
    const g3 = block(w, 'g3');
    expect(g3.text()).toContain('каскад');
    expect(g3.text()).toContain('временно переключена на запасной интерфейс');
    w.unmount();
  });

  it('тумблер правила -> POST enable, строка приглушается после перечитывания', async () => {
    const { w, f } = await makeView();
    const rows = block(w, 'g1').findAll('tbody tr');
    await rows[0].find('input[type=checkbox]').setValue(false);
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/mt/groups/g1/rules/r1/enable'));
      expect(post).toBeTruthy();
      expect(JSON.parse(post![1]!.body as string)).toEqual({ enable: false });
      expect(rows[0].classes()).toContain('dimmed');
    });
    w.unmount();
  });

  it('добавление правила: смена типа -> placeholder подсети, subnet без маски получает /32', async () => {
    const { w, f } = await makeView();
    const g1 = block(w, 'g1');
    await g1.find('.nrtype .psel-btn').trigger('click');
    await nextTick();
    await g1.findAll('.psel-item').filter((o) => o.text() === 'подсеть')[0].trigger('click');
    await nextTick();
    const val = g1.find('.nrval input');
    expect(val.attributes('placeholder')).toBe('149.154.160.0/20');
    await val.setValue('1.2.3.4');
    await btnIn(g1, '+ правило')[0].trigger('click');
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/mt/groups/g1/rules'));
      expect(post).toBeTruthy();
      expect(JSON.parse(post![1]!.body as string)).toEqual({ type: 'subnet', rule: '1.2.3.4/32', enable: true });
    });
    await vi.waitFor(() => expect(useToast().toasts.list.map((t) => t.text)).toContain('Правило добавлено'));
    w.unmount();
  });

  it('inline-редактор: держит editingLock, отмена закрывает, сохранить -> PUT с нормализацией', async () => {
    const { w, f } = await makeView();
    const rows = block(w, 'g1').findAll('tbody tr');
    await rows[0].find('button[title="изменить правило"]').trigger('click');
    await nextTick();
    expect(block(w, 'g1').find('.erow').exists()).toBe(true);
    expect(useGroups().state.editing).toBe(1); // перерендер отложен, пока открыто

    await btnIn(block(w, 'g1'), 'отмена')[0].trigger('click');
    await nextTick();
    expect(block(w, 'g1').find('.erow').exists()).toBe(false);
    expect(useGroups().state.editing).toBe(0);

    // строка после отмены пересоздана - перечитываем
    const rows2 = block(w, 'g1').findAll('tbody tr');
    await rows2[0].find('button[title="изменить правило"]').trigger('click');
    await nextTick();
    await block(w, 'g1').find('.erval input').setValue('9.9.9.9');
    // меняем тип на подсеть: 9.9.9.9 получит /32
    await block(w, 'g1').find('.erow .psel-btn').trigger('click');
    await nextTick();
    await block(w, 'g1').findAll('.psel-item').filter((o) => o.text() === 'подсеть')[0].trigger('click');
    await nextTick();
    await btnIn(block(w, 'g1'), 'сохранить')[0].trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => (c[0] as string).endsWith('/mt/groups/g1/rules/r1') && c[1]?.method === 'PUT');
      expect(put).toBeTruthy();
      expect(JSON.parse(put![1]!.body as string)).toEqual({ type: 'subnet', rule: '9.9.9.9/32', name: '' });
    });
    await vi.waitFor(() => expect(useGroups().state.editing).toBe(0));
    w.unmount();
  });

  it('DnD правил: drop после цели -> POST /rules/order с новым списком id', async () => {
    const { w, f } = await makeView();
    const rows = block(w, 'g1').findAll('tbody tr');
    await rows[0].find('.grip').trigger('pointerdown');
    await rows[0].trigger('dragstart');
    await rows[1].trigger('dragover', { clientY: 10 }); // rect нулевой в jsdom: >0 = «после»
    await rows[1].trigger('drop', { clientY: 10 });
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/mt/groups/g1/rules/order'));
      expect(post).toBeTruthy();
      expect(JSON.parse(post![1]!.body as string)).toEqual({ ids: ['r2', 'r1'] });
    });
    w.unmount();
  });

  it('DnD групп: порядок -> POST /mt/groups/order', async () => {
    const { w, f } = await makeView();
    await block(w, 'g1').find('.grip').trigger('pointerdown');
    await block(w, 'g1').trigger('dragstart');
    await block(w, 'g2').trigger('dragover', { clientY: 10 });
    await block(w, 'g2').trigger('drop', { clientY: 10 });
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/mt/groups/order'));
      expect(post).toBeTruthy();
      expect(JSON.parse(post![1]!.body as string)).toEqual({ ids: ['g2', 'g1', 'g3'] });
    });
    w.unmount();
  });
});

describe('GroupsView - поиск', () => {
  it('фильтр групп, автораскрытие, подсветка и «совпадений», пустой результат', async () => {
    const { w } = await makeView();
    await w.find('.sinput input').setValue('t.me');
    await nextTick();
    expect(w.findAll('.mtgroup')).toHaveLength(1);
    const g1 = block(w, 'g1');
    expect(g1.attributes('open') !== undefined || g1.find('details[open]').exists()).toBe(true);
    expect(g1.text()).toContain('совпадений: 1');
    expect(g1.findAll('mark')).toHaveLength(1); // t.me подсвечен

    await w.find('.sinput input').setValue('qqqq');
    await nextTick();
    expect(w.text()).toContain('Ничего не найдено по «qqqq».');
    w.unmount();
  });

  it('снапшот открытых групп: после очистки поиска открытость восстановлена', async () => {
    const { w } = await makeView();
    const det = block(w, 'g1').find('details');
    det.element.open = true; // jsdom не тогглит summary - ставим и шлём событие
    await det.trigger('toggle');
    await nextTick();

    await w.find('.sinput input').setValue('zzz');
    await nextTick();
    expect(w.findAll('.mtgroup')).toHaveLength(0);
    await w.find('.sinput input').setValue('');
    await nextTick();
    expect(block(w, 'g1').find('details').element.open).toBe(true);
    w.unmount();
  });
});

describe('GroupsView - создание/удаление/гистерезис', () => {
  it('создание группы: POST, очистка поля, строка появилась и автораскрыта', async () => {
    const { w, f } = await makeView();
    await w.find('.cname input').setValue('новая');
    await btnIn(w, '+ группа')[0].trigger('click');
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/mt/groups') && c[1]?.method === 'POST');
      expect(post).toBeTruthy();
      expect(JSON.parse(post![1]!.body as string)).toEqual({ name: 'новая', interface: 'nwg0', color: '#4a9eff' });
    });
    await vi.waitFor(() => expect(block(w, 'g9').exists()).toBe(true));
    expect(block(w, 'g9').find('details').element.open).toBe(true);
    expect((w.find('.cname input').element as HTMLInputElement).value).toBe('');
    w.unmount();
  });

  it('без имени - тост и без POST', async () => {
    const { w, f } = await makeView();
    await btnIn(w, '+ группа')[0].trigger('click');
    await nextTick();
    expect(useToast().toasts.list.map((t) => t.text)).toContain('Укажите имя группы и интерфейс');
    expect(f.mock.calls.filter((c) => (c[0] as string).endsWith('/mt/groups') && c[1]?.method === 'POST')).toHaveLength(0);
    w.unmount();
  });

  it('удаление группы: confirm, DELETE, строка ушла', async () => {
    const { w } = await makeView();
    await block(w, 'g1').find('button[title="удалить группу"]').trigger('click');
    await nextTick();
    expect(useConfirm().state.open).toBe(true);
    expect(useConfirm().state.text).toBe('Удалить группу telegram со всеми правилами?');
    useConfirm().ok();
    await vi.waitFor(() => expect(block(w, 'g1').exists()).toBe(false));
    w.unmount();
  });

  it('гистерезис: предзаполнен из GET, кнопка активна после изменения, PUT + тост', async () => {
    const { w, f } = await makeView();
    const nums = w.findAll('.cnum input');
    expect((nums[0].element as HTMLInputElement).value).toBe('2');
    expect((nums[1].element as HTMLInputElement).value).toBe('2');
    const save = btnIn(w, 'сохранить')[0];
    expect(save.attributes('disabled')).toBeDefined();
    await nums[0].setValue('3');
    await nextTick();
    expect(save.attributes('disabled')).toBeUndefined();
    await save.trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => (c[0] as string).endsWith('/mt/policy-cycles') && c[1]?.method === 'PUT');
      expect(put).toBeTruthy();
      expect(JSON.parse(put![1]!.body as string)).toEqual({ failCycles: 3, restoreCycles: 2 });
    });
    await vi.waitFor(() =>
      expect(useToast().toasts.list.map((t) => t.text).some((t) => t.includes('Гистерезис политик'))).toBe(true));
    w.unmount();
  });
});

describe('GroupsView - импорт текстом и дубликаты', () => {
  it('импорт правил: POST с опциями, тост-вердикт', async () => {
    const { w, f } = await makeView();
    await block(w, 'g1').findAll('button').filter((b) => b.text() === 'импорт')[0].trigger('click');
    await nextTick();
    const dlg = w.findAll('dialog').find((d) => d.find('h3').text() === 'Импорт правил в telegram')!;
    expect(dlg.text()).toContain('Усекать до второго уровня');
    await dlg.find('textarea').setValue('a.com');
    await btnIn(dlg, 'импортировать')[0].trigger('click');
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/mt/groups/g1/rules/import'));
      expect(post).toBeTruthy();
      expect(JSON.parse(post![1]!.body as string)).toEqual({ text: 'a.com', type: 'auto', toSecond: false, enable: true });
    });
    await vi.waitFor(() =>
      expect(useToast().toasts.list.map((t) => t.text)).toContain('Импортировано: 2, пропущено: 1. Отброшено: мусор'));
    w.unmount();
  });

  it('пустой список - тост без POST; дубликаты: диалог со сводкой', async () => {
    const { w, f } = await makeView({ 't.me': [{ groupName: 'ai', ruleType: 'domain' }, { groupName: 'backup', ruleType: 'domain' }] });
    await block(w, 'g1').findAll('button').filter((b) => b.text() === 'импорт')[0].trigger('click');
    await nextTick();
    const dlg = w.findAll('dialog').find((d) => d.find('h3').text() === 'Импорт правил в telegram')!;
    await btnIn(dlg, 'импортировать')[0].trigger('click');
    await nextTick();
    expect(useToast().toasts.list.map((t) => t.text)).toContain('Вставьте список');
    expect(f.mock.calls.filter((c) => (c[0] as string).endsWith('/rules/import'))).toHaveLength(0);

    await btnIn(w, 'показать совпадения в группах')[0].trigger('click');
    await nextTick();
    const dups = w.findAll('dialog').find((d) => d.find('h3').text() === 'Совпадения в группах')!;
    expect(dups.text()).toContain('t.me');
    expect(dups.text()).toContain('дубликат: 2 группы');
    expect(dups.text()).toContain('группы: ai, backup');
    w.unmount();
  });
});
