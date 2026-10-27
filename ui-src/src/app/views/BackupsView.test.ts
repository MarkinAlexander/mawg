import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import BackupsView from './BackupsView.vue'
import { dialogBackReset } from '../../components/ui/dialogBack'
import { useConfirm } from '../../stores/confirm'
import { useGroups } from '../../stores/groups'
import { useToast } from '../../stores/toast'
import type { GroupView } from '../../api/types'

const BACKUPS = [
  { name: 'mawg-full-20261014-030303.tar.gz', kind: 'full', size: 4321, modified: '2026-10-14 03:03' },
  { name: 'mt-shadow-20261014-010101.conf', kind: 'shadow', size: 1234, modified: '2026-10-14 01:01' },
  { name: 'mt-config-20261013-235900.conf', kind: 'config', size: 5678, modified: '2026-10-13 23:59' },
];

function groupsFixture(): GroupView[] {
  return [
    {
      id: 'g1', name: 'telegram', interface: 'nwg0', enable: true,
      rules: [{ id: 'r1', type: 'domain', rule: 't.me', enable: true }],
    },
    { id: 'g2', name: 'ai', interface: 'tun1', enable: true, rules: [] },
  ];
}

async function makeView() {
  const data = { backups: BACKUPS.map((b) => ({ ...b })), groups: groupsFixture(), imports: [] as unknown[] };
  const ok = (x: unknown = { ok: true }) => new Response(JSON.stringify(x), { status: 200 });
  const f = vi.fn(async (url: string, init?: { method?: string; body?: string }) => {
    const u = url;
    if (u.endsWith('/mt/backups')) return ok({ backups: data.backups });
    if (u.endsWith('/mt/backup-keep') && (init?.method || 'GET') === 'GET') return ok({ keep: 10 });
    if (u.endsWith('/mt/backup-keep') && init?.method === 'PUT') return ok({ keep: JSON.parse(init.body!).keep });
    if (u.endsWith('/mt/backup')) {
      data.backups.unshift({ name: 'mawg-full-20261014-040404.tar.gz', kind: 'full', size: 99, modified: '2026-10-14 04:04' });
      return ok();
    }
    if (u.endsWith('/mt/restore')) {
      const file = JSON.parse(init!.body!).file;
      data.groups = [{ id: 'g9', name: 'из бэкапа', interface: 'i', enable: true, rules: [] }];
      return ok({ ok: true, file });
    }
    if (u.endsWith('/mt/groups/import')) {
      const body = JSON.parse(init!.body!);
      data.imports.push(body);
      // бэкенд добавляет группы и назначает id сам - последующий
      // refresh (внутри importGroups) их вернёт
      for (const [i, g] of (body.groups || []).entries()) {
        data.groups.push({
          id: `g-new${data.imports.length}-${i}`, name: g.name, interface: g.interface,
          enable: g.enable ?? true, rules: g.rules || [],
        } as GroupView);
      }
      return ok({ added: 1, rules: 2, skipped: ['telegram'] });
    }
    if (u.endsWith('/mt/groups')) return ok({ available: true, groups: data.groups });
    return ok();
  });
  vi.stubGlobal('fetch', f);
  useGroups().state.groups = data.groups;
  const w = mount(BackupsView);
  await vi.waitFor(() => expect(useGroups().state.backups.loaded).toBe(true));
  await nextTick();
  return { w, f, data };
}

const btnIn = (
  scope: { findAll: (s: string) => ReturnType<ReturnType<typeof mount>['findAll']> },
  text: string,
) => scope.findAll('button').filter((b) => b.text() === text);
const card = (w: ReturnType<typeof mount>, id: string) => w.find(`#${id}`);

// jsdom-File не умеет text() - шимим через FileReader (в браузерах есть)
beforeAll(() => {
  if (typeof File.prototype.text !== 'function') {
    Object.defineProperty(File.prototype, 'text', {
      value(this: File) {
        return new Promise((res, rej) => {
          const fr = new FileReader();
          fr.onload = () => res(fr.result as string);
          fr.onerror = () => rej(fr.error);
          fr.readAsText(this);
        });
      },
    });
  }
});

beforeEach(() => {
  vi.unstubAllGlobals();
  dialogBackReset();
  const { state, stop } = useGroups();
  stop();
  state.groups = [];
  state.backups = { list: [], loaded: false };
  state.available = true;
  useConfirm().cancel();
  useToast().toasts.list.splice(0);
});

describe('BackupsView - серверные копии', () => {
  it('карточки разрешены (грабля забытого импорта), список грузится с kind-лейблами', async () => {
    const { w } = await makeView();
    expect(w.findAll('.card')).toHaveLength(2);
    expect(w.html()).not.toContain('<uicard');
    const backups = card(w, 'card-backups');
    const lines = backups.findAll('.bkline');
    expect(lines).toHaveLength(3);
    expect(lines[0].text()).toContain('полный');
    expect(lines[1].text()).toContain('shadow');
    expect(lines[2].text()).toContain('MT-конфиг');
    w.unmount();
  });

  it('создать бэкап -> POST + тост + перечитывание списка', async () => {
    const { w, f } = await makeView();
    const backups = card(w, 'card-backups');
    await btnIn(backups, 'создать бэкап сейчас')[0].trigger('click');
    await vi.waitFor(() => expect(f.mock.calls.some((c) => (c[0] as string).endsWith('/mt/backup'))).toBe(true));
    await vi.waitFor(() =>
      expect(useToast().toasts.list.map((t) => t.text)).toContain('Полный бэкап сохранён: настройки панели, правила и конфиг MT'));
    await vi.waitFor(() => expect(backups.findAll('.bkline')).toHaveLength(4)); // перечитан
    w.unmount();
  });

  it('восстановление: confirm по kind (shadow / MT-конфиг / полный), POST /mt/restore', async () => {
    const { w, f } = await makeView();
    const backups = card(w, 'card-backups');
    const restores = backups.findAll('button').filter((b) => b.text() === 'восстановить');

    // полный - честный текст про перезапуск и смену адреса
    await restores[0].trigger('click');
    await nextTick();
    expect(useConfirm().state.text).toContain('ПОЛНОЕ восстановление панели');
    expect(useConfirm().state.text).toContain('если порт или адрес в бэкапе другие');
    useConfirm().ok();
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/mt/restore'));
      expect(JSON.parse(post![1]!.body as string)).toEqual({ file: 'mawg-full-20261014-030303.tar.gz' });
    });

    await restores[1].trigger('click');
    await nextTick();
    expect(useConfirm().state.text).toContain('правила из shadow: ВСЕ группы будут заменены на сохранённые');
    useConfirm().ok();

    await restores[2].trigger('click');
    await nextTick();
    expect(useConfirm().state.text).toContain('сырой MT-конфиг: файл вернётся на место, демон MagiTrickle перезапустится');
    useConfirm().cancel();
    w.unmount();
  });
});

describe('BackupsView - импорт из файла', () => {
  async function pickFile(w: ReturnType<typeof mount>, content: string, name = 'groups.mtrickle'): Promise<void> {
    const input = w.findAll('input[type=file]')[0];
    const file = new File([content], name, { type: 'application/json' });
    Object.defineProperty(input.element, 'files', { value: [file], configurable: true });
    await input.trigger('change');
    await nextTick();
  }

  it('mt-config-* -> тост без списка; мусор -> тост', async () => {
    const { w } = await makeView();
    await pickFile(w, '{}', 'mt-config-2026.conf');
    await vi.waitFor(() => expect(useToast().toasts.list.map((t) => t.text)[0]).toContain('сырой MT-конфиг'));
    await pickFile(w, 'не json');
    await vi.waitFor(() => expect(useToast().toasts.list.map((t) => t.text)[1]).toContain('не похож на mtrickle/json'));
    expect(useToast().toasts.list.map((t) => t.text)).toHaveLength(2);
    expect(card(w, 'card-transfer').findAll('.bkline')).toHaveLength(2); // список групп, не импорта
    w.unmount();
  });

  it('инлайн-импорт: дубль имени снят в add, replace предупреждает, POST с дедупом, тост, секция свернулась', async () => {
    const { w, f } = await makeView();
    const transfer = card(w, 'card-transfer');
    const content = JSON.stringify({ groups: [
      { name: 'telegram', interface: 'nwg0', enable: true, rules: [
        { type: 'domain', rule: 't.me', enable: true },
        { type: 'domain', rule: 'T.ME', enable: true },
        { type: 'domain', rule: 'x.com', enable: false },
      ] },
      { name: 'новая', interface: 'nwg7', enable: false, rules: [{ type: 'subnet', rule: '1.2.3.4/32', enable: true }] },
    ] });
    await pickFile(w, content);
    await vi.waitFor(() => expect(transfer.text()).toContain('Файл groups.mtrickle: групп 2, правил 4'));
    // строки импорта: 2 группы в списке + выгрузка(2) - считаем по чекбоксам секции
    const impLines = transfer.findAll('.bkline').filter((l) => l.text().includes('дубль имени') || l.text().includes('новая'));
    expect(impLines).toHaveLength(2);
    const impBoxes = impLines.map((l) => l.find('input[type=checkbox]'));
    expect((impBoxes[0].element as HTMLInputElement).checked).toBe(false); // дубль снят
    expect((impBoxes[1].element as HTMLInputElement).checked).toBe(true);

    // replace: дубли больше не дубль, предупреждение видно
    const radios = transfer.findAll('input[type=radio]');
    await radios[1].setValue();
    await nextTick();
    expect(transfer.text()).toContain('Каскады перестроятся');
    expect((impBoxes[0].element as HTMLInputElement).checked).toBe(true);

    // назад в add и импортируем
    await radios[0].setValue();
    await nextTick();
    await btnIn(transfer, 'импортировать')[0].trigger('click');
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/mt/groups/import'));
      expect(post).toBeTruthy();
      const body = JSON.parse(post![1]!.body as string);
      expect(body.mode).toBe('add');
      expect(body.groups).toHaveLength(1); // только не-дубль
      expect(body.groups[0].name).toBe('новая');
      expect(body.groups[0].rules).toHaveLength(1);
    });
    await vi.waitFor(() =>
      expect(useToast().toasts.list.map((t) => t.text)).toContain('Импортировано групп: 1 (правил: 2). Пропущены дубли имени: telegram'));
    // успех - секция импорта сворачивается, кнопка снова «выбрать файл...»
    await vi.waitFor(() => expect(btnIn(transfer, 'выбрать файл...').length).toBe(1));
    w.unmount();
  });

  it('источники у импорта (Ф5§7): policy PUT на новые группы, пустой список - тост без POST, селектор сброшен', async () => {
    const { w, f } = await makeView();
    const transfer = card(w, 'card-transfer');
    const content = JSON.stringify({ groups: [
      { name: 'новая', interface: 'nwg7', enable: true, rules: [{ type: 'subnet', rule: '1.2.3.4/32', enable: true }] },
    ] });
    await pickFile(w, content);
    await vi.waitFor(() => expect(transfer.text()).toContain('Источники импортированных групп'));

    // «список IP» без адресов - тост, импорт не уходит
    await transfer.findAll('.psel-btn')[0].trigger('click');
    await transfer.findAll('.psel-item').filter((o) => o.text() === 'только выбранные IP')[0].trigger('click');
    await btnIn(transfer, 'импортировать')[0].trigger('click');
    await vi.waitFor(() =>
      expect(useToast().toasts.list.some((t) => t.text.includes('Пустой список источников'))).toBe(true));
    expect(f.mock.calls.some((c) => (c[0] as string).endsWith('/mt/groups/import'))).toBe(false);

    // «вся локальная сеть» - после импорта PUT policy на новую группу
    await transfer.findAll('.psel-btn')[0].trigger('click');
    await transfer.findAll('.psel-item').filter((o) => o.text() === 'вся локальная сеть')[0].trigger('click');
    await btnIn(transfer, 'импортировать')[0].trigger('click');
    await vi.waitFor(() => {
      const pol = f.mock.calls.find((c) => (c[0] as string).endsWith('/policy') && c[1]?.method === 'PUT');
      expect(pol).toBeTruthy();
      // вся политика целиком (GOTCHAS): у свежей группы onDead/iface пустые
      expect(JSON.parse(pol![1]!.body as string)).toEqual({
        onDead: '', iface: '', sourcesMode: 'all', sources: [], sourcesRouter: false,
      });
    });
    await vi.waitFor(() =>
      expect(useToast().toasts.list.some((t) => t.text.includes('источники заданы: 1'))).toBe(true));
    // секция свернулась, селектор сброшен
    await vi.waitFor(() => expect(btnIn(transfer, 'выбрать файл...').length).toBe(1));
    expect(transfer.text()).not.toContain('Источники импортированных групп');
    w.unmount();
  });

  it('источники у импорта: дубль имени пропущен - policy ему не уходит', async () => {
    const { w, f } = await makeView();
    const transfer = card(w, 'card-transfer');
    const content = JSON.stringify({ groups: [
      { name: 'telegram', interface: 'nwg0', enable: true, rules: [] },
    ] });
    await pickFile(w, content);
    await vi.waitFor(() => expect(transfer.text()).toContain('Источники импортированных групп'));
    // дубль снят заранее; включаем его вручную - импорт пропустит имя
    // (локатор - строка импорта с бейджем «дубль имени», не выгрузка)
    const box = transfer.findAll('.bkline').filter((l) => l.text().includes('дубль имени'))[0].find('input[type=checkbox]');
    await box.setValue();
    await transfer.findAll('.psel-btn')[0].trigger('click');
    await transfer.findAll('.psel-item').filter((o) => o.text() === 'локальная сеть + роутер')[0].trigger('click');
    await btnIn(transfer, 'импортировать')[0].trigger('click');
    await vi.waitFor(() => expect(f.mock.calls.some((c) => (c[0] as string).endsWith('/mt/groups/import'))).toBe(true));
    await vi.waitFor(() =>
      expect(useToast().toasts.list.some((t) => t.text.includes('Пропущены дубли имени: telegram'))).toBe(true));
    await vi.waitFor(() => expect(btnIn(transfer, 'выбрать файл...').length).toBe(1));
    expect(f.mock.calls.some((c) => (c[0] as string).endsWith('/policy') && c[1]?.method === 'PUT')).toBe(false);
    w.unmount();
  });
});

describe('BackupsView - выгрузка на ПК', () => {
  it('инлайн: чекбоксы все отмечены, снять все -> disabled, скачать -> Blob с выбранными, пустые группы - hint', async () => {
    const created: unknown[] = [];
    vi.stubGlobal('URL', {
      ...URL,
      createObjectURL: vi.fn((blob: Blob) => {
        created.push(blob);
        return 'blob:mock';
      }),
      revokeObjectURL: vi.fn(),
    });
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
    const { w } = await makeView();
    const transfer = card(w, 'card-transfer');

    // без групп - hint (временно опустошаем стор)
    const { state } = useGroups();
    const saved = state.groups;
    state.groups = [];
    await nextTick();
    expect(transfer.text()).toContain('Групп нет - выгружать нечего');
    state.groups = saved;
    await nextTick();

    const expLines = transfer.findAll('.bkline').filter((l) => l.text().includes('правил:'));
    expect(expLines).toHaveLength(2);
    expect((expLines[0].find('input[type=checkbox]').element as HTMLInputElement).checked).toBe(true);
    await btnIn(transfer, 'снять все')[0].trigger('click');
    await nextTick();
    expect(btnIn(transfer, 'скачать файл (0)')[0].attributes('disabled')).toBeDefined();
    await expLines[0].find('input[type=checkbox]').setValue(true);
    await nextTick();
    await btnIn(transfer, 'скачать файл (1)')[0].trigger('click');
    await nextTick();
    expect(created).toHaveLength(1);
    const blobText = await new Promise<string>((res, rej) => {
      const fr = new FileReader();
      fr.onload = () => res(fr.result as string);
      fr.onerror = () => rej(fr.error);
      fr.readAsText(created[0] as Blob);
    });
    expect(blobText).toContain('"telegram"');
    expect(useToast().toasts.list.map((t) => t.text)).toContain('Выгружено групп: 1');
    clickSpy.mockRestore();
    w.unmount();
  });
});

describe('BackupsView - ретеншен и сжатие', () => {
  it('поле «сколько копий хранить»: предзаполнено, применять -> PUT keep, subtitle честный', async () => {
    const { w, f } = await makeView();
    const numInput = () => w.findAll('input').find((i) => i.attributes('type') === 'number');
    await vi.waitFor(() => expect(numInput()?.element.value).toBe('10'));
    const card = w.findAll('.card').find((c) => c.text().includes('Резервные копии'))!;
    expect(card.text()).toContain('по 10 последних каждого вида');
    expect(card.text()).toContain('сжатыми');

    await numInput()!.setValue('3');
    const apply = w.findAll('button').find((b) => b.text() === 'применить')!;
    expect(apply.attributes('disabled')).toBeUndefined();
    await apply.trigger('click');
    await vi.waitFor(() => {
      const put = f.mock.calls.find((c) => c[0].endsWith('/mt/backup-keep') && c[1]?.method === 'PUT');
      expect(put).toBeTruthy();
      expect(JSON.parse(String(put![1]!.body)).keep).toBe(3);
    });
    w.unmount();
  });

  it('вне диапазона 2..10 - тост с подсказкой, PUT не уходит', async () => {
    const { w, f } = await makeView();
    await vi.waitFor(() => expect(w.findAll('input').find((i) => i.attributes('type') === 'number')?.element.value).toBe('10'));
    await w.findAll('input').find((i) => i.attributes('type') === 'number')!.setValue('11');
    await w.findAll('button').find((b) => b.text() === 'применить')!.trigger('click');
    await nextTick();
    expect(useToast().toasts.list.some((t) => t.text.includes('от 2 до 10'))).toBe(true);
    expect(f.mock.calls.find((c) => c[0].endsWith('/mt/backup-keep') && c[1]?.method === 'PUT')).toBeUndefined();
    w.unmount();
  });
});
