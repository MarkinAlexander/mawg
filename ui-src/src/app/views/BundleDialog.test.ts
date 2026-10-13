import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import BundleDialog from './BundleDialog.vue'
import { useBundles } from '../../stores/bundles'
import { useGroups } from '../../stores/groups'
import { useIfaces } from '../../stores/ifaces'
import { useStatus } from '../../stores/status'
import { useToast } from '../../stores/toast'
import type { BundleView, GroupView, Status } from '../../api/types'

function stub(routes: Record<string, unknown>) {
  return vi.fn(async (url: string, init?: { method?: string; body?: string }) => {
    const key = Object.keys(routes).find((k) => (url as string).endsWith(k));
    const v = key ? routes[key] : {};
    const body = typeof v === 'function' ? (v as (i?: typeof init) => unknown)(init) : v;
    return new Response(JSON.stringify(body), { status: 200 });
  });
}

const editing: BundleView = {
  name: 'main', members: ['proton', 'nwg7'], groups: ['g1'],
  currentMember: 'proton', memberDevices: { proton: 'nwg0', nwg7: 'nwg7' },
};

const groups: GroupView[] = [
  { id: 'g1', name: 'telegram', interface: 'nwg0', enable: true },
  { id: 'g2', name: 'ai', interface: 'tun1', enable: true },
];

function seed(over: Partial<Status> = {}) {
  useStatus().state.data = {
    version: 'v', platform: 'keenetic', backendName: 'keenetic', authEnabled: false,
    nativeAWG3: false, mtSources: true,
    pools: [
      { name: 'proton', platform: 'keenetic', device: 'nwg0', slot: 'nwg0', mode: 'up', activeFile: '', activeEndpoint: '', lastResult: '', lastError: '', rotations: 0, disabled: false, consecFails: 0, settings: {}, configs: [], premium: { imported: false, countries: [] }, protos: [] },
      { name: 'warp', platform: 'keenetic', device: 'nwg9', slot: 'nwg9', mode: 'up', activeFile: '', activeEndpoint: '', lastResult: '', lastError: '', rotations: 0, disabled: false, consecFails: 0, settings: {}, configs: [], premium: { imported: false, countries: [] }, protos: [] },
    ],
    engine: undefined, ...over,
  } as Status;
  const ifs = useIfaces();
  ifs.state.list = [
    { device: 'nwg7', description: 'провайдер', mode: 'external', linkUp: true, connected: true, handshakeAgo: 5, probe: null },
    { device: 'tun1', description: '', mode: 'hidden', linkUp: true, connected: true, handshakeAgo: -1, probe: null },
  ];
  ifs.state.loaded = true;
  useGroups().state.groups = groups;
}

async function makeDlg(editing: BundleView | null, routes: Record<string, unknown> = {}) {
  seed();
  const f = stub(routes);
  vi.stubGlobal('fetch', f);
  const dlg = mount(BundleDialog, { props: { editing, open: false } });
  await dlg.setProps({ open: true });
  await nextTick();
  return { dlg, f };
}

const btn = (w: ReturnType<typeof mount>, text: string) =>
  w.findAll('button').filter((b) => b.text() === text);

beforeEach(() => {
  vi.unstubAllGlobals();
  useBundles().stop();
  useBundles().state.list = [];
  useStatus().stop();
  useStatus().state.data = null;
  const gs = useGroups();
  gs.stop();
  gs.state.groups = [];
  const ifs = useIfaces();
  ifs.stop();
  ifs.state.list = [];
  useToast().toasts.list.splice(0);
});

describe('BundleDialog', () => {
  it('новый: кандидаты - пулы и внешние интерфейсы (скрытые - нет), пустые списки', () => {
    const w = mount(BundleDialog, { props: { editing: null, open: true } });
    expect(w.text()).toContain('Новый набор');
    // селект добавления рендерится лениво - открываем
    void w.find('.psel-btn').trigger('click');
    // psel-item рендерится после клика
    expect(w.text()).toContain('Участники по приоритету');
    expect(w.text()).toContain('Группы MagiTrickle в наборе');
    w.unmount();
  });

  it('правка: имя задизейблено и предзаполнено, тумблер группы g1 включён', async () => {
    seed();
    const w = mount(BundleDialog, { props: { editing, open: false } });
    await w.setProps({ open: true });
    await nextTick();
    const nameInput = w.findAll('input').find((i) => (i.element as HTMLInputElement).value === 'main')!;
    expect(nameInput.attributes('disabled')).toBeDefined();
    const boxes = w.findAll('input[type=checkbox]');
    // g1 в наборе - checked, g2 - нет
    const states = boxes.map((b) => (b.element as HTMLInputElement).checked);
    expect(states).toEqual([true, false]);
    expect(w.text()).toContain('telegram');
    expect(w.text()).toContain('(nwg0)');
    w.unmount();
  });

  it('валидации: без имени/участников/групп - тосты без POST', async () => {
    const { dlg, f } = await makeDlg(null);
    await btn(dlg, 'Сохранить')[0].trigger('click');
    expect(useToast().toasts.list.map((t) => t.text)).toContain('Укажите имя');
    await dlg.findAll('input').find((i) => i.attributes('type') !== 'checkbox')!.setValue('b1');
    await btn(dlg, 'Сохранить')[0].trigger('click');
    expect(useToast().toasts.list.map((t) => t.text)).toContain('Добавьте хотя бы одного участника');
    // добавляем участника
    await dlg.find('.psel-btn').trigger('click');
    await nextTick();
    await dlg.findAll('.psel-item')[0].trigger('click');
    await nextTick();
    await btn(dlg, 'добавить')[0].trigger('click');
    await nextTick();
    await btn(dlg, 'Сохранить')[0].trigger('click');
    expect(useToast().toasts.list.map((t) => t.text)).toContain('Выберите хотя бы одну группу');
    expect(f.mock.calls.filter((c) => (c[1]?.method === 'POST'))).toHaveLength(0);
    dlg.unmount();
  });

  it('создание: участники reorder, группы тумблерами, Сохранить -> POST /bundles', async () => {
    const { dlg, f } = await makeDlg(null);
    await dlg.findAll('input').find((i) => i.attributes('type') !== 'checkbox')!.setValue('b1');
    // добавить proton
    await dlg.find('.psel-btn').trigger('click');
    await nextTick();
    await dlg.findAll('.psel-item').find((o) => o.text().includes('proton'))!.trigger('click');
    await nextTick();
    await btn(dlg, 'добавить')[0].trigger('click');
    await nextTick();
    // добавить nwg7
    await dlg.find('.psel-btn').trigger('click');
    await nextTick();
    await dlg.findAll('.psel-item').find((o) => o.text().includes('nwg7'))!.trigger('click');
    await nextTick();
    await btn(dlg, 'добавить')[0].trigger('click');
    await nextTick();
    // proton теперь первый; поднять nwg7 выше
    const rows = dlg.findAll('.memrow');
    expect(rows).toHaveLength(2);
    expect(rows[0].text()).toContain('proton (пул)');
    await rows[1].findAll('button').find((b) => b.attributes('title') === 'выше')!.trigger('click');
    await nextTick();
    expect(dlg.findAll('.memrow')[0].text()).toContain('nwg7');
    // включить обе группы
    for (const cb of dlg.findAll('input[type=checkbox]')) await cb.setValue(true);
    await btn(dlg, 'Сохранить')[0].trigger('click');
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/bundles') && c[1]?.method === 'POST');
      expect(JSON.parse(post![1]!.body as string)).toEqual({ name: 'b1', members: ['nwg7', 'proton'], groups: ['g1', 'g2'] });
    });
    dlg.unmount();
  });

  it('правка: PUT /bundles/{name} с исходным именем набора', async () => {
    const { dlg, f } = await makeDlg(editing);
    // выключить g1, включить g2
    const boxes = dlg.findAll('input[type=checkbox]');
    await boxes[0].setValue(false);
    await boxes[1].setValue(true);
    await btn(dlg, 'Сохранить')[0].trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => (c[0] as string).endsWith('/bundles/main') && c[1]?.method === 'PUT');
      expect(JSON.parse(put![1]!.body as string)).toEqual({ name: 'main', members: ['proton', 'nwg7'], groups: ['g2'] });
    });
    dlg.unmount();
  });
});
