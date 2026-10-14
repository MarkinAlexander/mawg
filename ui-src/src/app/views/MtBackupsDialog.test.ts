import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import MtBackupsDialog from './MtBackupsDialog.vue'
import { useConfirm } from '../../stores/confirm'
import { useGroups } from '../../stores/groups'
import { useToast } from '../../stores/toast'
import type { GroupView } from '../../api/types'

const BACKUPS = [
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

async function makeDlg() {
  const data = { backups: BACKUPS.map((b) => ({ ...b })), groups: groupsFixture(), imports: [] as unknown[] };
  const ok = (x: unknown = { ok: true }) => new Response(JSON.stringify(x), { status: 200 });
  const f = vi.fn(async (url: string, init?: { method?: string; body?: string }) => {
    const u = url;
    if (u.endsWith('/mt/backups')) return ok({ backups: data.backups });
    if (u.endsWith('/mt/backup')) {
      data.backups.unshift({ name: 'mt-shadow-20261014-020202.conf', kind: 'shadow', size: 99, modified: '2026-10-14 02:02' });
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
      return ok({ added: 1, rules: 2, skipped: ['telegram'] });
    }
    if (u.endsWith('/mt/groups')) return ok({ available: true, groups: data.groups });
    return ok();
  });
  vi.stubGlobal('fetch', f);
  useGroups().state.groups = data.groups;
  const openRef = ref(false);
  const w = mount(defineComponent(() => () => [
    h(MtBackupsDialog, { open: openRef.value, 'onUpdate:open': (v: boolean) => { openRef.value = v; } }),
  ]));
  openRef.value = true;
  await nextTick();
  await vi.waitFor(() => expect(useGroups().state.backups.loaded).toBe(true));
  await nextTick();
  return { w, f, data };
}

const btnIn = (
  scope: { findAll: (s: string) => ReturnType<ReturnType<typeof mount>['findAll']> },
  text: string,
) => scope.findAll('button').filter((b) => b.text() === text);
const openDlg = (w: ReturnType<typeof mount>, title: string) =>
  w.findAll('dialog').find((d) => d.find('h3').text() === title)!;

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
  const { state, stop } = useGroups();
  stop();
  state.groups = [];
  state.backups = { list: [], loaded: false };
  state.available = true;
  useConfirm().cancel();
  useToast().toasts.list.splice(0);
});

describe('MtBackupsDialog - список бэкапов', () => {
  it('открытие грузит список, kind-лейблы, создать бэкап -> POST + тост + перечитывание', async () => {
    const { w, f, data } = await makeDlg();
    const dlg = openDlg(w, 'Резервные копии правил');
    expect(dlg.text()).toContain('хранятся последние 10');
    const lines = dlg.findAll('.bkline');
    expect(lines).toHaveLength(2);
    expect(lines[0].text()).toContain('shadow');
    expect(lines[1].text()).toContain('MT-конфиг');

    await btnIn(dlg, 'создать бэкап сейчас')[0].trigger('click');
    await vi.waitFor(() => expect(f.mock.calls.some((c) => (c[0] as string).endsWith('/mt/backup'))).toBe(true));
    await vi.waitFor(() =>
      expect(useToast().toasts.list.map((t) => t.text)).toContain('Бэкап MT-конфига и shadow сохранён в base/backups'));
    await vi.waitFor(() => expect(dlg.findAll('.bkline')).toHaveLength(3)); // перечитан
    void data;
    w.unmount();
  });

  it('восстановление shadow: confirm по kind, POST /mt/restore, группы перечитаны', async () => {
    const { w, f } = await makeDlg();
    const dlg = openDlg(w, 'Резервные копии правил');
    await btnIn(dlg, 'восстановить')[0].trigger('click');
    await nextTick();
    expect(useConfirm().state.title).toBe('Восстановление из бэкапа');
    expect(useConfirm().state.text).toBe('Восстановить mt-shadow-20261014-010101.conf? Применится: правила из shadow: ВСЕ группы будут заменены на сохранённые.');
    useConfirm().ok();
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/mt/restore'));
      expect(JSON.parse(post![1]!.body as string)).toEqual({ file: 'mt-shadow-20261014-010101.conf' });
    });
    await vi.waitFor(() =>
      expect(useToast().toasts.list.map((t) => t.text)).toContain('Восстановлено из mt-shadow-20261014-010101.conf'));
    w.unmount();
  });

  it('mt-config восстановление - другой текст confirm', async () => {
    const { w } = await makeDlg();
    const dlg = openDlg(w, 'Резервные копии правил');
    await btnIn(dlg, 'восстановить')[1].trigger('click'); // вторая строка = config
    await nextTick();
    expect(useConfirm().state.text).toContain('сырой MT-конфиг: файл вернётся на место, демон MagiTrickle перезапустится');
    useConfirm().cancel();
    w.unmount();
  });
});

describe('MtBackupsDialog - импорт из файла', () => {
  async function pickFile(w: ReturnType<typeof mount>, content: string, name = 'groups.mtrickle'): Promise<void> {
    const input = w.findAll('input[type=file]')[0];
    const file = new File([content], name, { type: 'application/json' });
    Object.defineProperty(input.element, 'files', { value: [file], configurable: true });
    await input.trigger('change');
    await nextTick();
  }

  it('mt-config-* -> тост без диалога; мусор -> тост', async () => {
    const { w } = await makeDlg();
    await pickFile(w, '{}', 'mt-config-2026.conf');
    await vi.waitFor(() => expect(useToast().toasts.list.map((t) => t.text)[0]).toContain('сырой MT-конфиг'));
    await pickFile(w, 'не json');
    await vi.waitFor(() => expect(useToast().toasts.list.map((t) => t.text)[1]).toContain('не похож на mtrickle/json'));
    expect(useToast().toasts.list.map((t) => t.text)).toHaveLength(2);
    const d = openDlg(w, 'Импорт групп из файла');
    expect(d.attributes('open')).toBeUndefined();
    w.unmount();
  });

  it('диалог импорта: дубль имени снят в add, replace снимает дубль-логику и предупреждает, POST с дедупом, тост', async () => {
    const { w, f } = await makeDlg();
    const content = JSON.stringify({ groups: [
      { name: 'telegram', interface: 'nwg0', enable: true, rules: [
        { type: 'domain', rule: 't.me', enable: true },
        { type: 'domain', rule: 'T.ME', enable: true },
        { type: 'domain', rule: 'x.com', enable: false },
      ] },
      { name: 'новая', interface: 'nwg7', enable: false, rules: [{ type: 'subnet', rule: '1.2.3.4/32', enable: true }] },
    ] });
    await pickFile(w, content);
    await vi.waitFor(() => {
      const dlg = openDlg(w, 'Импорт групп из файла');
      expect(dlg.text()).toContain('Файл groups.mtrickle: групп 2, правил 4');
    });
    const dlg = openDlg(w, 'Импорт групп из файла');
    expect(dlg.attributes('open')).toBeDefined();
    const boxes = dlg.findAll('input[type=checkbox]');
    expect((boxes[0].element as HTMLInputElement).checked).toBe(false); // дубль имени снят
    expect((boxes[1].element as HTMLInputElement).checked).toBe(true);
    expect(dlg.text()).toContain('дубль имени');

    // replace: дубли больше не дубль, предупреждение видно
    const radios = dlg.findAll('input[type=radio]');
    await radios[1].setValue();
    await nextTick();
    expect(dlg.text()).toContain('Каскады перестроятся');
    expect((dlg.findAll('input[type=checkbox]')[0].element as HTMLInputElement).checked).toBe(true);

    // назад в add и импортируем
    await dlg.findAll('input[type=radio]')[0].setValue();
    await nextTick();
    await btnIn(dlg, 'импортировать')[0].trigger('click');
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
    w.unmount();
  });
});

describe('MtBackupsDialog - выгрузка на ПК', () => {
  it('без групп - тост; с группами: снять все -> disabled, скачать -> Blob с выбранными', async () => {
    const urls: string[] = [];
    const created: unknown[] = [];
    vi.stubGlobal('URL', {
      ...URL,
      createObjectURL: vi.fn((blob: Blob) => {
        created.push(blob);
        return 'blob:mock';
      }),
      revokeObjectURL: vi.fn(),
    });
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {
      urls.push('clicked');
    });
    const { w } = await makeDlg();
    const dlg = openDlg(w, 'Резервные копии правил');

    // без групп тост (временно опустошаем стор)
    const { state } = useGroups();
    const saved = state.groups;
    state.groups = [];
    await btnIn(dlg, 'выгрузить группы в файл')[0].trigger('click');
    expect(useToast().toasts.list.map((t) => t.text)).toContain('Групп нет - выгружать нечего');
    state.groups = saved;

    await btnIn(dlg, 'выгрузить группы в файл')[0].trigger('click');
    await nextTick();
    const exp = openDlg(w, 'Выгрузка групп в файл');
    expect(exp.text()).toContain('вечная копия');
    const boxes = exp.findAll('input[type=checkbox]');
    expect((boxes[0].element as HTMLInputElement).checked).toBe(true);
    await btnIn(exp, 'снять все')[0].trigger('click');
    expect(btnIn(exp, 'скачать файл')[0].attributes('disabled')).toBeDefined();
    await boxes[0].setValue(true);
    await btnIn(exp, 'скачать файл')[0].trigger('click');
    await nextTick();
    expect(created).toHaveLength(1);
    expect(created).toHaveLength(1);
    const blobText = await new Promise<string>((res, rej) => {
      const fr = new FileReader();
      fr.onload = () => res(fr.result as string);
      fr.onerror = () => rej(fr.error);
      fr.readAsText(created[0] as Blob);
    });
    expect(blobText).toContain('"telegram"');
    expect(urls).toEqual(['clicked']);
    expect(useToast().toasts.list.map((t) => t.text)).toContain('Выгружено групп: 1');
    clickSpy.mockRestore();
    w.unmount();
  });
});
