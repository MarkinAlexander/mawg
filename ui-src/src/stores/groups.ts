// Группы MagiTrickle: GET /mt/groups каждые 30с с кэшем по JSON-хэшу и
// editingLock (legacy: editingRulesPanel/pendingRulesRender - во время
// правки правил перерендер откладывается, при выходе из правки применяется).
// Мутации - тонкие обёртки: вызов API + force-refresh.
import { reactive } from 'vue'
import { api } from '../api/client'
import type { GroupPolicy, GroupView, MtGroupsRes } from '../api/types'

interface GroupsState {
  loaded: boolean
  available: boolean
  groups: GroupView[]
  glitchy: boolean
  wrapper: boolean
  sourcesCapable: boolean
  duplicates: Record<string, string[]>
  editing: number // глубина editingLock
  pending: boolean // есть неотрисованные изменения (правка открыта)
  lastJSON: string
}

const state = reactive<GroupsState>({
  loaded: false,
  available: true,
  groups: [],
  glitchy: false,
  wrapper: false,
  sourcesCapable: true,
  duplicates: {},
  editing: 0,
  pending: false,
  lastJSON: '',
});

let timer: ReturnType<typeof setInterval> | null = null;
let inflight = false;

export function useGroups() {
  async function refresh(force = false): Promise<void> {
    if (inflight) return;
    inflight = true;
    try {
      const r = await api<MtGroupsRes>('GET', '/mt/groups');
      if (!r.available) {
        state.available = false;
        return;
      }
      state.available = true;
      state.glitchy = !!r.glitchy;
      state.wrapper = !!r.wrapper;
      state.sourcesCapable = r.sources !== false;
      const json = JSON.stringify(r.groups || []);
      const changed = force || json !== state.lastJSON;
      state.lastJSON = json;
      if (!changed) return;
      if (state.editing > 0) {
        state.pending = true; // правка открыта - применить после выхода
        return;
      }
      state.groups = r.groups || [];
      state.loaded = true;
    } finally {
      inflight = false;
    }
  }

  // editingLock: карточки правил поднимают/опускают счётчик; на нуле
  // подтягивается отложенное (промис - чтобы UI/тесты могли дождаться)
  function setEditing(delta: number): Promise<void> | undefined {
    state.editing = Math.max(0, state.editing + delta);
    if (state.editing === 0 && state.pending) {
      state.pending = false;
      return refresh(true);
    }
    return undefined;
  }

  async function refreshDuplicates(): Promise<void> {
    try {
      const r = await api<{ duplicates: Record<string, string[]> }>('GET', '/mt/duplicates');
      state.duplicates = r.duplicates || {};
    } catch {
      state.duplicates = {};
    }
  }

  function start(intervalMs = 30000) {
    void refresh(true);
    void refreshDuplicates();
    if (timer) return;
    timer = setInterval(() => void refresh(), intervalMs);
  }
  function stop() {
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
  }

  // --- мутации: API, затем force-refresh ---
  async function createGroup(body: { name: string; interface: string; color?: string; sources?: string[] }): Promise<GroupView | null> {
    const g = await api<GroupView>('POST', '/mt/groups', body);
    await refresh(true);
    return g;
  }
  async function updateGroup(id: string, body: { name?: string; color?: string; interface?: string; sources?: string[] }): Promise<void> {
    await api('PUT', `/mt/groups/${encodeURIComponent(id)}`, body);
    await refresh(true);
  }
  async function deleteGroup(id: string): Promise<void> {
    await api('DELETE', `/mt/groups/${encodeURIComponent(id)}?save=true`);
    await refresh(true);
  }
  async function toggleGroup(id: string, enable: boolean): Promise<void> {
    await api('POST', `/mt/groups/${encodeURIComponent(id)}/enable`, { enable });
    await refresh(true);
  }
  async function reorderGroups(ids: string[]): Promise<void> {
    await api('POST', '/mt/groups/order', { ids });
    await refresh(true);
  }
  async function setPolicy(id: string, policy: GroupPolicy): Promise<void> {
    await api('PUT', `/mt/groups/${encodeURIComponent(id)}/policy`, policy);
    await refresh(true);
  }
  async function addRule(groupId: string, rule: { type: string; rule: string }): Promise<void> {
    await api('POST', `/mt/groups/${encodeURIComponent(groupId)}/rules`, { ...rule, enable: true });
    await refresh(true);
  }
  async function importRules(groupId: string, text: string): Promise<void> {
    await api('POST', `/mt/groups/${encodeURIComponent(groupId)}/rules/import`, { text });
    await refresh(true);
  }
  async function updateRule(groupId: string, ruleId: string, rule: Partial<{ type: string; rule: string; name: string; enable: boolean }>): Promise<void> {
    await api('PUT', `/mt/groups/${encodeURIComponent(groupId)}/rules/${encodeURIComponent(ruleId)}`, rule);
    await refresh(true);
  }
  async function toggleRule(groupId: string, ruleId: string, enable: boolean): Promise<void> {
    await api('POST', `/mt/groups/${encodeURIComponent(groupId)}/rules/${encodeURIComponent(ruleId)}/enable`, { enable });
    await refresh(true);
  }
  async function deleteRule(groupId: string, ruleId: string): Promise<void> {
    await api('DELETE', `/mt/groups/${encodeURIComponent(groupId)}/rules/${encodeURIComponent(ruleId)}`);
    await refresh(true);
  }
  async function reorderRules(groupId: string, ids: string[]): Promise<void> {
    await api('POST', `/mt/groups/${encodeURIComponent(groupId)}/rules/order`, { ids });
    await refresh(true);
  }

  return {
    state,
    refresh,
    refreshDuplicates,
    setEditing,
    start,
    stop,
    createGroup,
    updateGroup,
    deleteGroup,
    toggleGroup,
    reorderGroups,
    setPolicy,
    addRule,
    importRules,
    updateRule,
    toggleRule,
    deleteRule,
    reorderRules,
  };
}
