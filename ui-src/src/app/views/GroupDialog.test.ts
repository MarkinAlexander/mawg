import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import GroupDialog from './GroupDialog.vue'
import { UiColor } from '../../components/ui'
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
async function makeDlg(group: GroupView | null, ifaces: { device: string; title?: string }[] = [{ device: 'nwg0', title: 'пул proton' }], capable = true) {
  const f = vi.fn(async (_url: string, _init?: { method?: string; body?: string }) => new Response(JSON.stringify({ ok: true }), { status: 200 }));
  vi.stubGlobal('fetch', f);
  const st = useGroups().state;
  st.ifaces = ifaces;
  st.sourcesCapable = capable;
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
  state.sourcesCapable = true;
  useToast().toasts.list.splice(0);
});

describe('GroupDialog', () => {
  it('предзаполнен: имя/цвет/интерфейс с титулом, sources list-mode с роутером', async () => {
    const { w } = await makeDlg(GRP);
    const dlg = dlgOf(w);
    expect(dlg.find('h3').text()).toBe('Изменить группу');
    const name = dlg.find('input[type=text]');
    expect((name.element as HTMLInputElement).value).toBe('telegram');
    expect(dlg.findComponent(UiColor).props('modelValue')).toBe('#2AABEE');
    const selBtns = dlg.findAll('.psel-btn');
    // интерфейс + кто ходит + политика
    expect(selBtns.length).toBe(3);
    expect(selBtns[0].text()).toContain('nwg0 - пул proton');
    expect(selBtns[1].text()).toContain('только выбранные IP');
    expect(selBtns[2].text()).toContain('идти напрямую');
    // список источников и галка роутера видны в режиме list
    const ipInput = dlg.findAll('input').find((i) => i.attributes('placeholder')?.includes('192.168.1.50'));
    expect((ipInput!.element as HTMLInputElement).value).toBe('192.168.1.50');
    const boxes = dlg.findAll('input[type=checkbox]');
    expect((boxes[boxes.length - 1].element as HTMLInputElement).checked).toBe(true);
    w.unmount();
  });

  it('битый цвет -> дефолт #4a9eff; у каскадной интерфейс залочен и sources скрыты', async () => {
    const { w } = await makeDlg({ ...GRP, color: 'red', cascade: { group: 'g1', source: 'pool:warp', via: 'iface:nwg7' } });
    const dlg = dlgOf(w);
    expect(dlg.findComponent(UiColor).props('modelValue')).toBe('#4a9eff');
    const ifaceBtn = dlg.findAll('.psel-btn')[0];
    expect(ifaceBtn.attributes('disabled')).toBeDefined(); // залочен у каскада
    expect(dlg.findAll('.psel-btn').length).toBe(2); // интерфейс + политика, sources скрыты
    expect(dlg.html()).toContain('задается при создании каскада'); // tooltip в title подписи
    w.unmount();
  });

  it('blackhole: политика скрыта (sources видны), Сохранить шлёт onDead:""', async () => {
    const { w, f } = await makeDlg({ ...GRP, interface: 'blackhole' });
    const dlg = dlgOf(w);
    expect(dlg.text()).toContain('Политика на случай пропажи канала не применяется');
    expect(dlg.findAll('.psel-btn').length).toBe(2); // интерфейс + кто ходит
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

  it('Сохранить: PUT группы + PUT политики (sources из диалога), onDead=iface открывает запасной', async () => {
    const { w, f } = await makeDlg(GRP);
    const dlg = dlgOf(w);
    // политику (третий селект) на запасной интерфейс
    await dlg.findAll('.psel-btn')[2].trigger('click');
    await nextTick();
    await dlg.findAll('.psel-item').filter((o) => o.text().includes('запасной интерфейс'))[0].trigger('click');
    await nextTick();
    // запасной интерфейс: nwg9 не в списке интерфейсов - добавлен опцией
    const ifBtns = dlg.findAll('.psel-btn');
    expect(ifBtns.length).toBe(4);
    await ifBtns[3].trigger('click');
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

  it('list без IP и без роутера - тост «Пустой список», PUT policy не уходит', async () => {
    const { w, f } = await makeDlg(GRP);
    const dlg = dlgOf(w);
    const ipInput = dlg.findAll('input').find((i) => i.attributes('placeholder')?.includes('192.168.1.50'))!;
    await ipInput.setValue('');
    const boxes = dlg.findAll('input[type=checkbox]');
    await boxes[boxes.length - 1].setValue(false);
    await btnIn(dlg, 'Сохранить')[0].trigger('click');
    await nextTick();
    expect(useToast().toasts.list.map((t) => t.text)).toContain('Пустой список: добавьте IP или включите роутер');
    expect(f.mock.calls.filter((c) => (c[0] as string).endsWith('/policy'))).toHaveLength(0);
    w.unmount();
  });

  it('смена режима на «вся локалка» прячет список, в PUT уходит sourcesMode all', async () => {
    const { w, f } = await makeDlg({ ...GRP, policy: { sourcesMode: 'list', sources: ['1.2.3.4'] } });
    const dlg = dlgOf(w);
    await dlg.findAll('.psel-btn')[1].trigger('click');
    await nextTick();
    await dlg.findAll('.psel-item').filter((o) => o.text() === 'вся локальная сеть')[0].trigger('click');
    await nextTick();
    expect(dlg.findAll('input').some((i) => i.attributes('placeholder')?.includes('192.168.1.50'))).toBe(false);
    await btnIn(dlg, 'Сохранить')[0].trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => (c[0] as string).endsWith('/mt/groups/g1/policy'));
      expect(JSON.parse(put![1]!.body as string)).toEqual({
        // легаси шлёт распарсенный список и в не-list режимах - сервер игнорирует
        onDead: '', iface: '', sourcesMode: 'all', sources: ['1.2.3.4'], sourcesRouter: false,
      });
    });
    w.unmount();
  });

  it('старый MT без sources: блок скрыт, подсказка, Сохранить шлёт mode all', async () => {
    const { w, f } = await makeDlg(GRP, undefined, false);
    const dlg = dlgOf(w);
    expect(dlg.text()).toContain('не поддерживает источники');
    expect(dlg.findAll('.psel-btn').length).toBe(2); // интерфейс + политика
    await btnIn(dlg, 'Сохранить')[0].trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => (c[0] as string).endsWith('/mt/groups/g1/policy'));
      expect(JSON.parse(put![1]!.body as string)).toEqual({
        onDead: 'direct', iface: 'nwg9', sourcesMode: 'all', sources: [], sourcesRouter: false,
      });
    });
    w.unmount();
  });
});
