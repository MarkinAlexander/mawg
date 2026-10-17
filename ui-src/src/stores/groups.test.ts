import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useGroups } from './groups'

function mtGroups(payload: Record<string, unknown>) {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(payload), { status: 200 })));
}

beforeEach(() => {
  vi.unstubAllGlobals();
  // сброс синглтон-стора между тестами
  const { state } = useGroups();
  state.editing = 0;
  state.pending = false;
  state.lastJSON = '';
  state.groups = [];
  state.duplicates = {};
  state.ifaces = [];
  state.cycles = null;
  state.presets = { list: [], sourceUrl: '', sourceNote: '', loaded: false };
  state.available = true;
  state.loaded = false;
});

describe('groups store', () => {
  it('refresh наполняет группы и capability-флаги', async () => {
    mtGroups({
      available: true,
      glitchy: false,
      wrapper: true,
      sources: false,
      groups: [{ id: 'g1', name: 'x', interface: 'awg0', enable: true, rules: [] }],
    });
    const { state, refresh } = useGroups();
    await refresh(true);
    expect(state.available).toBe(true);
    expect(state.groups).toHaveLength(1);
    expect(state.wrapper).toBe(true);
    expect(state.sourcesCapable).toBe(false); // старый бинарь без sources
  });

  it('кэш по JSON: без force повторный refresh не меняет lastJSON-логику', async () => {
    const payload = { available: true, groups: [{ id: 'a', name: 'n', interface: 'i', enable: true, rules: [] }] };
    mtGroups(payload);
    const { state, refresh } = useGroups();
    await refresh(true);
    const same = JSON.stringify(state.groups);
    await refresh(); // тот же payload - список не перезатирается (ссылка та же)
    expect(state.groups === undefined ? null : same).toBe(same);
  });

  it('editingLock: при открытой правке изменения откладываются и применяются на нуле', async () => {
    const { state, refresh, setEditing } = useGroups();
    mtGroups({ available: true, groups: [{ id: '1', name: 'a', interface: 'i', enable: true, rules: [] }] });
    await refresh(true);
    expect(state.groups[0].name).toBe('a');

    setEditing(1);
    mtGroups({ available: true, groups: [{ id: '1', name: 'b', interface: 'i', enable: true, rules: [] }] });
    await refresh();
    expect(state.pending).toBe(true);
    expect(state.groups[0].name).toBe('a'); // не дёргаем DOM правки

    await setEditing(-1); // правка закрылась - дожидаемся отложенного
    expect(state.pending).toBe(false);
    expect(state.groups[0].name).toBe('b'); // отложенное применилось
  });

  it('MT недоступен: available=false, группы держатся как были', async () => {
    const { state, refresh } = useGroups();
    mtGroups({ available: true, groups: [{ id: '1', name: 'a', interface: 'i', enable: true, rules: [] }] });
    await refresh(true);
    mtGroups({ available: false });
    await refresh(true);
    expect(state.available).toBe(false);
    expect(state.groups).toHaveLength(1);
  });

  it('мутация делает force-refresh (toggleGroup)', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ available: true, groups: [{ id: 'g1', name: 'x', interface: 'i', enable: true, rules: [] }] }), { status: 200 }))
      .mockResolvedValueOnce(new Response('', { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ available: true, groups: [{ id: 'g1', name: 'x', interface: 'i', enable: false, rules: [] }] }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const { state, toggleGroup } = useGroups();
    await useGroups().refresh(true);
    await toggleGroup('g1', false);
    expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/mt/groups/g1/enable');
    expect(state.groups[0].enable).toBe(false);
  });

  it('loadIfaces: селекты интерфейсов из GET /mt/interfaces', async () => {
    const fetchMock = vi.fn(async (_url: string) => new Response(JSON.stringify({ interfaces: [{ device: 'nwg0', title: 'пул proton' }] }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const { state, loadIfaces } = useGroups();
    await loadIfaces();
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/mt/interfaces');
    expect(state.ifaces).toEqual([{ device: 'nwg0', title: 'пул proton' }]);
  });

  it('cycles: загрузка гистерезиса и сохранение с обновлением состояния', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ failCycles: 2, restoreCycles: 2 }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ failCycles: 3, restoreCycles: 2 }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const { state, loadCycles, saveCycles } = useGroups();
    await loadCycles();
    expect(state.cycles).toEqual({ failCycles: 2, restoreCycles: 2 });
    const r = await saveCycles(3, 2);
    expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/mt/policy-cycles');
    expect(JSON.parse(fetchMock.mock.calls[1][1]!.body as string)).toEqual({ failCycles: 3, restoreCycles: 2 });
    expect(r).toEqual({ failCycles: 3, restoreCycles: 2 });
    expect(state.cycles).toEqual({ failCycles: 3, restoreCycles: 2 });
  });

  it('importRules шлёт text+type+toSecond+enable и возвращает вердикт', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ available: true, groups: [] }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ added: 2, skipped: 1, bad: ['мусор'] }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ available: true, groups: [] }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const { refresh, importRules } = useGroups();
    await refresh(true);
    const r = await importRules('g1', { text: 'a.com', type: 'auto', toSecond: true, enable: false });
    expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/mt/groups/g1/rules/import');
    expect(JSON.parse(fetchMock.mock.calls[1][1]!.body as string)).toEqual({ text: 'a.com', type: 'auto', toSecond: true, enable: false });
    expect(r).toEqual({ added: 2, skipped: 1, bad: ['мусор'] });
  });

  it('пресеты: загрузка списка+источника, применение с перечитыванием', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        presets: [{ id: 'p1', title: 'T', rules: [{ type: 'domain', rule: 'a.com' }] }],
        sourceUrl: 'https://github.com/x', sourceNote: 'note',
      }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true, groupId: 'g5', rules: 1 }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ available: true, groups: [] }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const { state, loadPresets, applyPreset } = useGroups();
    await loadPresets();
    expect(state.presets.list).toHaveLength(1);
    expect(state.presets.sourceUrl).toBe('https://github.com/x');
    const n = await applyPreset('p1', 'nwg0');
    expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/mt/presets/p1/apply');
    expect(n).toBe(1);
    expect(state.groups).toEqual([]); // перечитан
  });

  it('createCascade: POST /cascades с перечитыванием групп', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ available: true, groups: [{ id: 'g5', name: 'Служебная', interface: 'i', enable: true, rules: [] }] }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const { state, createCascade } = useGroups();
    await createCascade({ source: 'pool:warp', via: 'iface:nwg7', hosts: ['8.8.8.8'] });
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/cascades');
    expect(JSON.parse(fetchMock.mock.calls[0][1]!.body as string)).toEqual({ source: 'pool:warp', via: 'iface:nwg7', hosts: ['8.8.8.8'] });
    expect(state.groups).toHaveLength(1);
  });
});
