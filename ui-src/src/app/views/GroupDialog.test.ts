import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import GroupDialog from './GroupDialog.vue'
import { useGroups } from '../../stores/groups'
import { useToast } from '../../stores/toast'
import type { GroupView } from '../../api/types'

const GRP: GroupView = {
  id: 'g1', name: 'telegram', color: '#2AABEE', interface: 'nwg0', enable: true,
  policy: { onDead: 'direct', iface: 'nwg9', sourcesMode: 'list', sources: ['192.168.1.50'], sourcesRouter: true },
  rules: [],
};

// open=false при монтировании, потом взводим - как реальное открытие
// (watch(open) заполняет форму)
async function makeDlg(group: GroupView | null, ifaces: { device: string; title?: string }[] = [{ device: 'nwg0', title: 'пул proton' }]) {
  const f = vi.fn(async (_url: string, _init?: { method?: string; body?: string }) => new Response(JSON.stringify({ ok: true }), { status: 200 }));
  vi.stubGlobal('fetch', f);
  useGroups().state.ifaces = ifaces;
  const openRef = ref(false);
  const w = mount(defineComponent(() => () => [
    h(GroupDialog, { group, open: openRef.value, 'onUpdate:open': (v: boolean) => { openRef.value = v; } }),
  ]));
  openRef.value = true;
  await nextTick();
  return { w, f };
}

const dlgOf = (w: ReturnType<typeof mount>) => w.find('dialog');
const btnIn = (scope: { findAll: (s: string) => ReturnType<ReturnType<typeof mount>['findAll']> }, text: string) =>
  scope.findAll('button').filter((b) => b.text() === text);

beforeEach(() => {
  vi.unstubAllGlobals();
  const { state } = useGroups();
  state.ifaces = [];
  useToast().toasts.list.splice(0);
});

describe('GroupDialog', () => {
  it('предзаполнен: имя/цвет/интерфейс с титулом, политика iface - запасной в опциях', async () => {
    const { w } = await makeDlg(GRP);
    const dlg = dlgOf(w);
    expect(dlg.find('h3').text()).toBe('Изменить группу');
    const name = dlg.find('input[type=text]');
    expect((name.element as HTMLInputElement).value).toBe('telegram');
    expect((dlg.find('input[type=color]').element as HTMLInputElement).value.toLowerCase()).toBe('#2aabee');
    expect(dlg.find('.psel-btn').text()).toContain('nwg0 - пул proton');
    // политика: onDead=direct - селект показать не должен запасной (только при iface)
    const selBtns = dlg.findAll('.psel-btn');
    expect(selBtns.length).toBe(2); // интерфейс + политика
    expect(selBtns[1].text()).toContain('прямой ход');
    w.unmount();
  });

  it('битый цвет -> дефолт #4a9eff; у каскадной интерфейс залочен', async () => {
    const { w } = await makeDlg({ ...GRP, color: 'red', cascade: { group: 'g1', source: 'pool:warp', via: 'iface:nwg7' } });
    const dlg = dlgOf(w);
    expect((dlg.find('input[type=color]').element as HTMLInputElement).value).toBe('#4a9eff');
    const ifaceBtn = dlg.findAll('.psel-btn')[0];
    expect(ifaceBtn.attributes('disabled')).toBeDefined(); // залочен у каскада
    w.unmount();
  });

  it('blackhole: политика скрыта, Сохранить шлёт onDead:"" с сохранением sources', async () => {
    const { w, f } = await makeDlg({ ...GRP, interface: 'blackhole' });
    const dlg = dlgOf(w);
    expect(dlg.text()).toContain('политика отвала не применяется');
    expect(dlg.findAll('.psel-btn').length).toBe(1); // только интерфейс
    await btnIn(dlg, 'Сохранить')[0].trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => (c[0] as string).endsWith('/mt/groups/g1/policy'));
      expect(put).toBeTruthy();
      expect(JSON.parse(put![1]!.body as string)).toEqual({
        onDead: '', iface: '', sourcesMode: 'list', sources: ['192.168.1.50'], sourcesRouter: true,
      });
    });
    w.unmount();
  });

  it('Сохранить: PUT группы + PUT политики (sources нетронуты), onDead=iface открывает запасной', async () => {
    const { w, f } = await makeDlg(GRP);
    const dlg = dlgOf(w);
    // политику на запасной интерфейс: селект политики -> iface
    await dlg.findAll('.psel-btn')[1].trigger('click');
    await nextTick();
    await dlg.findAll('.psel-item').filter((o) => o.text().includes('запасной интерфейс'))[0].trigger('click');
    await nextTick();
    // запасной интерфейс: nwg9 не в списке интерфейсов - добавлен опцией
    const ifBtns = dlg.findAll('.psel-btn');
    expect(ifBtns.length).toBe(3);
    await ifBtns[2].trigger('click');
    await nextTick();
    await dlg.findAll('.psel-item').filter((o) => o.text() === 'nwg9')[0].trigger('click');
    await nextTick();

    await dlg.find('input[type=text]').setValue('tg2');
    await btnIn(dlg, 'Сохранить')[0].trigger('click');
    await vi.waitFor(() => {
      const putG = f.mock.calls.find((c) => (c[0] as string).endsWith('/mt/groups/g1') && c[1]?.method === 'PUT');
      expect(putG).toBeTruthy();
      expect(JSON.parse(putG![1]!.body as string)).toEqual({ name: 'tg2', color: '#2AABEE', interface: 'nwg0' });
      const putP = f.mock.calls.find((c) => (c[0] as string).endsWith('/mt/groups/g1/policy'));
      expect(putP).toBeTruthy();
      expect(JSON.parse(putP![1]!.body as string)).toEqual({
        onDead: 'iface', iface: 'nwg9', sourcesMode: 'list', sources: ['192.168.1.50'], sourcesRouter: true,
      });
    });
    w.unmount();
  });
});
