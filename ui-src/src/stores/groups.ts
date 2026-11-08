// Группы MagiTrickle: GET /mt/groups каждые 30с с кэшем по JSON-хэшу и
// editingLock (legacy: editingRulesPanel/pendingRulesRender - во время
// правки правил перерендер откладывается, при выходе из правки применяется).
// Мутации - тонкие обёртки: вызов API + force-refresh.
import { reactive } from 'vue'
import { api } from '../api/client'
import type { ApplyDraftRes, AutoSaveRes, BackupInfo, DeviceBinding, DupInfo, DuplicatesRes, GroupPolicy, GroupView, ImportRulesRes, MtDraft, MtGroupsRes, MtIface, PolicyCycles, PresetsRes } from '../api/types'

interface GroupsState {
  loaded: boolean
  available: boolean
  groups: GroupView[]
  glitchy: boolean
  wrapper: boolean
  backendFlavor: string // семейство бэкенда MT (107-док): mawg-c | mawg-dev | upstream
  sourcesCapable: boolean
  duplicates: Record<string, DupInfo[]>
  ifaces: MtIface[] // селекты интерфейсов (легаси MT_IFACES, опрос 60с)
  cycles: PolicyCycles | null // гистерезис политик (легаси loadPolicyCycles)
  excludes: string[] // исключения из обработки (94-док): общий список
  excludesLoaded: boolean
  bindings: DeviceBinding[] // реестр «пул ↔ устройство» (97-док)
  bindingsLoaded: boolean
  presets: { list: PresetsRes['presets']; sourceUrl: string; sourceNote: string; loaded: boolean }
  backups: { list: BackupInfo[]; loaded: boolean }
  autoSave: boolean // 114-док: автосохранение - каждое действие сразу пишется в MT
  draft: MtDraft // черновик записей (114-док): active - ждёт «Сохранить», dirty - число изменений
  busy: boolean // мутация записей в полёте (114-док: при автосохранении список блокируется)
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
  backendFlavor: '',
  sourcesCapable: true,
  duplicates: {},
  ifaces: [],
  cycles: null,
  excludes: [],
  excludesLoaded: false,
  bindings: [],
  bindingsLoaded: false,
  presets: { list: [], sourceUrl: '', sourceNote: '', loaded: false },
  backups: { list: [], loaded: false },
  autoSave: false, // истина придёт с первым /mt/groups; до него мутаций всё равно не видно
  draft: { active: false, dirty: 0 },
  busy: false,
  editing: 0,
  pending: false,
  lastJSON: '',
});

let timer: ReturnType<typeof setInterval> | null = null;
let ifacesTimer: ReturnType<typeof setInterval> | null = null;
let inflight = false;

// мутационные ответы групп/правил содержат свежий draft (114-док) - в стор
// сразу, не дожидаясь force-refresh; ответ без draft (пусто/не мутация MT)
// стор не трогает - черновик догонит перечитывание
function takeDraft(r: unknown): void {
  const d = (r as { draft?: MtDraft | null } | undefined)?.draft;
  if (!d) return;
  state.draft = { active: !!d.active, dirty: d.dirty || 0, groupIds: d.groupIds || [], ruleIds: d.ruleIds || [] };
}

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
      state.backendFlavor = r.backendFlavor || '';
      state.sourcesCapable = r.sources !== false;
      // 114-док: режим автосохранения и черновик приходят вместе с группами;
      // до кэш-проверки списка - смена черновика без смены групп тоже видна.
      // Поля нет (переходный старый бэкенд) - не затираем draft, полученный
      // из ответа мутации
      if (r.autoSave !== undefined) state.autoSave = !!r.autoSave;
      if (r.draft) state.draft = { active: !!r.draft.active, dirty: r.draft.dirty || 0, groupIds: r.draft.groupIds || [], ruleIds: r.draft.ruleIds || [] };
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

  // мутация записей MT под busy (114-док, слово владельца 08.11): busy живёт
  // до конца force-refresh - при автосохранении оверлей «MT занят, пишем…»
  // снимается, когда список уже показывает результат
  async function mutate<T>(call: () => Promise<T>): Promise<T> {
    state.busy = true;
    try {
      const r = await call();
      takeDraft(r);
      await refresh(true);
      return r;
    } finally {
      state.busy = false;
    }
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

  // исключения из обработки (94-док): старт - молча, мутация - с вердиктом
  async function loadExcludes(): Promise<void> {
    try {
      const r = await api<{ excludes: string[] }>('GET', '/excludes');
      state.excludes = r.excludes || [];
      state.excludesLoaded = true;
    } catch { /* список просто останется пустым */ }
  }
  async function saveExcludes(list: string[]): Promise<string[]> {
    const r = await api<{ excludes: string[] }>('PUT', '/excludes', { excludes: list });
    state.excludes = r.excludes || [];
    return state.excludes;
  }

  // реестр «пул ↔ устройство через пул» (97-док): ленивая догрузка,
  // как excludes (PoolCard/настройки пула показывают устройства)
  async function loadBindings(): Promise<void> {
    try {
      const r = await api<{ bindings: DeviceBinding[] }>('GET', '/device-bindings');
      state.bindings = r.bindings || [];
      state.bindingsLoaded = true;
    } catch { /* реестр просто останется пустым */ }
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
    const r = await mutate(() => api<{ rules: number }>('POST', `/mt/presets/${encodeURIComponent(id)}/apply`, { interface: iface }));
    return r.rules;
  }

  // создание каскада (легаси csCreate 2098)
  async function createCascade(body: { source: string; via: string; hosts: string[] }): Promise<void> {
    await mutate(() => api('POST', '/cascades', body));
  }

  // сколько копий каждого вида бэкапа хранить (2..10)
  async function loadBackupKeep(): Promise<number> {
    const r = await api<{ keep: number }>('GET', '/mt/backup-keep');
    return r.keep;
  }
  async function saveBackupKeep(keep: number): Promise<number> {
    const r = await api<{ keep: number }>('PUT', '/mt/backup-keep', { keep });
    return r.keep;
  }

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
    await mutate(() => api('POST', '/mt/restore', { file }));
  }
  async function importGroups(mode: 'add' | 'replace', groups: unknown): Promise<{ added: number; rules: number; skipped?: string[] }> {
    return mutate(() => api<{ added: number; rules: number; skipped?: string[] }>('POST', '/mt/groups/import', { mode, groups }));
  }

  // автосохранение (114-док, слово владельца 08.11): 409 - активный черновик,
  // включать нельзя; ошибка уходит наверх - тост и откат тумблера решает UI
  async function setAutoSave(enable: boolean): Promise<AutoSaveRes> {
    const r = await api<AutoSaveRes>('POST', '/mt/autosave', { enable });
    state.autoSave = r.autoSave;
    state.draft = { active: !!r.draft?.active, dirty: r.draft?.dirty || 0, groupIds: r.draft?.groupIds || [], ruleIds: r.draft?.ruleIds || [] };
    await refresh(true);
    return r;
  }
  // применить черновик одной записью (114-док); 404 уходит наверх - панель
  // черновика видна только при draft.active, до неё это гонка
  // «Сохранить»/«Сбросить» - тоже запись в MT: busy блокирует список (114-док);
  // статус черновика сбрасывается НЕМЕДЛЕННО - ждать перечитывания нельзя
  // (кейс владельца 08.11: панель «Сбросить» висела до следующего опроса)
  async function applyDraft(): Promise<number> {
    state.busy = true;
    try {
      const r = await api<ApplyDraftRes>('POST', '/mt/apply-draft');
      state.draft = { active: false, dirty: 0, groupIds: [], ruleIds: [] };
      await refresh(true);
      return r.applied;
    } finally {
      state.busy = false;
    }
  }
  async function discardDraft(): Promise<void> {
    state.busy = true;
    try {
      await api('POST', '/mt/discard-draft');
      state.draft = { active: false, dirty: 0, groupIds: [], ruleIds: [] };
      await refresh(true);
    } finally {
      state.busy = false;
    }
  }

  function start(intervalMs = 30000) {
    void refresh(true);
    void refreshDuplicates();
    void loadIfaces();
    void loadCycles();
    void loadExcludes();
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

  async function createGroup(body: { name: string; interface: string; color?: string; sources?: string[] }): Promise<GroupView | null> {
    return mutate(() => api<GroupView>('POST', '/mt/groups', body));
  }
  // группа «устройство целиком через пул» (88+: sources + 0.0.0.0/0,
  // ставится последней в списке - побеждает при пересечениях).
  // confirm - подтверждение коллизии адресов (97-док, 409 с сервера)
  async function createDeviceGroup(body: { pool: string; name?: string; ips: string[]; confirm?: boolean }): Promise<void> {
    await mutate(() => api('POST', '/mt/device-group', body));
    await loadBindings();
  }
  // снять привязку устройства: удалить MT-группу + запись реестра
  async function deleteDeviceGroup(name: string): Promise<void> {
    await mutate(() => api('DELETE', `/mt/device-group/${encodeURIComponent(name)}`));
    await loadBindings();
  }
  async function updateGroup(id: string, body: { name?: string; color?: string; interface?: string; sources?: string[] }): Promise<void> {
    await mutate(() => api('PUT', `/mt/groups/${encodeURIComponent(id)}`, body));
  }
  async function deleteGroup(id: string): Promise<void> {
    await mutate(() => api('DELETE', `/mt/groups/${encodeURIComponent(id)}?save=true`));
  }
  async function toggleGroup(id: string, enable: boolean): Promise<void> {
    await mutate(() => api('POST', `/mt/groups/${encodeURIComponent(id)}/enable`, { enable }));
  }
  async function reorderGroups(ids: string[]): Promise<void> {
    await mutate(() => api('POST', '/mt/groups/order', { ids }));
  }
  async function setPolicy(id: string, policy: GroupPolicy): Promise<void> {
    await mutate(() => api('PUT', `/mt/groups/${encodeURIComponent(id)}/policy`, policy));
  }
  async function addRule(groupId: string, rule: { type: string; rule: string }): Promise<void> {
    await mutate(() => api('POST', `/mt/groups/${encodeURIComponent(groupId)}/rules`, { ...rule, enable: true }));
  }
  // импорт правил текстом (легаси шлёт {text, type, toSecond, enable})
  async function importRules(groupId: string, body: { text: string; type?: string; toSecond?: boolean; enable?: boolean }): Promise<ImportRulesRes> {
    return mutate(() => api<ImportRulesRes>('POST', `/mt/groups/${encodeURIComponent(groupId)}/rules/import`, body));
  }
  async function updateRule(groupId: string, ruleId: string, rule: Partial<{ type: string; rule: string; name: string; enable: boolean }>): Promise<void> {
    await mutate(() => api('PUT', `/mt/groups/${encodeURIComponent(groupId)}/rules/${encodeURIComponent(ruleId)}`, rule));
  }
  async function toggleRule(groupId: string, ruleId: string, enable: boolean): Promise<void> {
    await mutate(() => api('POST', `/mt/groups/${encodeURIComponent(groupId)}/rules/${encodeURIComponent(ruleId)}/enable`, { enable }));
  }
  // массовое включение/выключение всех правил группы (один PUT группы на
  // бэкенде); возвращает {total, changed} - для тоста
  async function enableAllRules(groupId: string, enable: boolean): Promise<{ total: number; changed: number }> {
    const r = await mutate(() => api<{ ok: boolean; total: number; changed: number }>(
      'POST', `/mt/groups/${encodeURIComponent(groupId)}/rules/enable`, { enable }));
    return { total: r.total, changed: r.changed };
  }
  async function deleteRule(groupId: string, ruleId: string): Promise<void> {
    await mutate(() => api('DELETE', `/mt/groups/${encodeURIComponent(groupId)}/rules/${encodeURIComponent(ruleId)}`));
  }
  async function reorderRules(groupId: string, ids: string[]): Promise<void> {
    await mutate(() => api('POST', `/mt/groups/${encodeURIComponent(groupId)}/rules/order`, { ids }));
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
    loadExcludes,
    saveExcludes,
    loadBindings,
    deleteDeviceGroup,
    loadPresets,
    applyPreset,
    createCascade,
    loadBackups,
    loadBackupKeep,
    saveBackupKeep,
    createBackup,
    restoreBackup,
    importGroups,
    createGroup,
    createDeviceGroup,
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
    setAutoSave,
    applyDraft,
    discardDraft,
  };
}
