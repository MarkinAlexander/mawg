// Группы MagiTrickle: GET /mt/groups каждые 30с с кэшем по JSON-хэшу и
// editingLock (legacy: editingRulesPanel/pendingRulesRender - во время
// правки правил перерендер откладывается, при выходе из правки применяется).
// Мутации - тонкие обёртки: вызов API + force-refresh.
import { reactive } from 'vue'
import { api } from '../api/client'
import type { BackupInfo, DupInfo, DuplicatesRes, GroupPolicy, GroupView, ImportRulesRes, MtGroupsRes, MtIface, PolicyCycles, PresetsRes } from '../api/types'

interface GroupsState {
  loaded: boolean
  available: boolean
  groups: GroupView[]
  glitchy: boolean
  wrapper: boolean
  sourcesCapable: boolean
  duplicates: Record<string, DupInfo[]>
  ifaces: MtIface[] // селекты интерфейсов (легаси MT_IFACES, опрос 60с)
  cycles: PolicyCycles | null // гистерезис политик (легаси loadPolicyCycles)
  presets: { list: PresetsRes['presets']; sourceUrl: string; sourceNote: string; loaded: boolean }
  backups: { list: BackupInfo[]; loaded: boolean }
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
  ifaces: [],
  cycles: null,
  presets: { list: [], sourceUrl: '', sourceNote: '', loaded: false },
  backups: { list: [], loaded: false },
  editing: 0,
  pending: false,
  lastJSON: '',
});

let timer: ReturnType<typeof setInterval> | null = null;
let ifacesTimer: ReturnType<typeof setInterval> | null = null;
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
    } catch {
      // сеть - как легаси: группы недоступны, держим прошлый список;
      // refresh зовут и fire-and-forget (диалог наборов) - не бросаем наверх
      state.available = false;
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
      const r = await api<DuplicatesRes>('GET', '/mt/duplicates');
      state.duplicates = r.duplicates || {};
    } catch {
      state.duplicates = {};
    }
  }

  // интерфейсы для селектов группы (легаси fillIfaceSelects: старт + 60с)
  async function loadIfaces(): Promise<void> {
    try {
      const r = await api<{ interfaces: MtIface[] }>('GET', '/mt/interfaces');
      state.ifaces = r.interfaces || [];
    } catch {
      state.ifaces = [];
    }
  }

  // гистерезис политик (легаси loadPolicyCycles: старт, ошибки молча)
  async function loadCycles(): Promise<void> {
    try {
      state.cycles = await api<PolicyCycles>('GET', '/mt/policy-cycles');
    } catch {
      /* поле просто останется пустым */
    }
  }
  async function saveCycles(failCycles: number, restoreCycles: number): Promise<PolicyCycles> {
    const r = await api<PolicyCycles>('PUT', '/mt/policy-cycles', { failCycles, restoreCycles });
    state.cycles = r;
    return r;
  }

  // пресеты (легаси loadPresets 2149: старт, без поллинга, ошибки молча)
  async function loadPresets(): Promise<void> {
    try {
      const r = await api<PresetsRes>('GET', '/mt/presets');
      state.presets.list = r.presets || [];
      state.presets.sourceUrl = r.sourceUrl || '';
      state.presets.sourceNote = r.sourceNote || '';
      state.presets.loaded = true;
    } catch { /* список просто не появится */ }
  }

  // применение пресета -> новая группа (легаси presetApply 2255)
  async function applyPreset(id: string, iface: string): Promise<number> {
    const r = await api<{ rules: number }>('POST', `/mt/presets/${encodeURIComponent(id)}/apply`, { interface: iface });
    await refresh(true);
    return r.rules;
  }

  // создание каскада (легаси csCreate 2098)
  async function createCascade(body: { source: string; via: string; hosts: string[] }): Promise<void> {
    await api('POST', '/cascades', body);
    await refresh(true);
  }

  // --- бэкапы и импорт/выгрузка групп файлом (легаси 1786-2010) ---
  async function loadBackups(): Promise<void> {
    try {
      const r = await api<{ backups: BackupInfo[] }>('GET', '/mt/backups');
      state.backups.list = r.backups || [];
      state.backups.loaded = true;
    } catch {
      state.backups.list = [];
      state.backups.loaded = true;
    }
  }
  async function createBackup(): Promise<void> {
    await api('POST', '/mt/backup');
    await loadBackups();
  }
  async function restoreBackup(file: string): Promise<void> {
    await api('POST', '/mt/restore', { file });
    await refresh(true);
  }
  async function importGroups(mode: 'add' | 'replace', groups: unknown): Promise<{ added: number; rules: number; skipped?: string[] }> {
    const r = await api<{ added: number; rules: number; skipped?: string[] }>('POST', '/mt/groups/import', { mode, groups });
    await refresh(true);
    return r;
  }

  function start(intervalMs = 30000) {
    void refresh(true);
    void refreshDuplicates();
    void loadIfaces();
    void loadCycles();
    if (timer) return;
    timer = setInterval(() => void refresh(), intervalMs);
    ifacesTimer = setInterval(() => void loadIfaces(), 60000);
  }
  function stop() {
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
    if (ifacesTimer) {
      clearInterval(ifacesTimer);
      ifacesTimer = null;
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
  // импорт правил текстом (легаси шлёт {text, type, toSecond, enable})
  async function importRules(groupId: string, body: { text: string; type?: string; toSecond?: boolean; enable?: boolean }): Promise<ImportRulesRes> {
    const r = await api<ImportRulesRes>('POST', `/mt/groups/${encodeURIComponent(groupId)}/rules/import`, body);
    await refresh(true);
    return r;
  }
  async function updateRule(groupId: string, ruleId: string, rule: Partial<{ type: string; rule: string; name: string; enable: boolean }>): Promise<void> {
    await api('PUT', `/mt/groups/${encodeURIComponent(groupId)}/rules/${encodeURIComponent(ruleId)}`, rule);
    await refresh(true);
  }
  async function toggleRule(groupId: string, ruleId: string, enable: boolean): Promise<void> {
    await api('POST', `/mt/groups/${encodeURIComponent(groupId)}/rules/${encodeURIComponent(ruleId)}/enable`, { enable });
    await refresh(true);
  }
  // массовое включение/выключение всех правил группы (один PUT группы на
  // бэкенде); возвращает {total, changed} - для тоста
  async function enableAllRules(groupId: string, enable: boolean): Promise<{ total: number; changed: number }> {
    const r = await api<{ ok: boolean; total: number; changed: number }>(
      'POST', `/mt/groups/${encodeURIComponent(groupId)}/rules/enable`, { enable });
    await refresh(true);
    return { total: r.total, changed: r.changed };
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
    loadIfaces,
    loadCycles,
    saveCycles,
    loadPresets,
    applyPreset,
    createCascade,
    loadBackups,
    createBackup,
    restoreBackup,
    importGroups,
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
    enableAllRules,
    deleteRule,
    reorderRules,
  };
}
