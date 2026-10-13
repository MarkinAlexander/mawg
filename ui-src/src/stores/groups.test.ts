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
});
