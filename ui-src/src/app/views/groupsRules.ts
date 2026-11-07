// Композаблы карточки «Правила маршрутизации» (Ф3.7): ref-состояние и
// обработчики, вынесенные из GroupsView.vue (создание/пресеты, исключения,
// гистерезис, список групп с поиском/DnD/правками). Чистые функции без
// состояния - в groupsLogic.ts; здесь - то, что держит refs/store/watch.
import { computed, nextTick, reactive, ref, watch, type InjectionKey, type Ref } from 'vue'
import { useRouter } from 'vue-router'
import { dropIndex, isBeforeHalf, reorder } from '../../components/ui/dnd'
import type { UiSelectOption } from '../../components/ui'
import { useConfirm } from '../../stores/confirm'
import { useGroups } from '../../stores/groups'
import { useStatus } from '../../stores/status'
import { useToast } from '../../stores/toast'
import type { GroupView, Preset, Rule } from '../../api/types'
import {
  cyclesValid, groupMatches, hideOffGroups, hideOffRules, normRuleValue,
  offGroupsTotal, offRulesTotal, presetRulesLine, showAllRules,
} from './groupsLogic'

// контекст списка групп (provide/inject из GroupsView): биндинги шаблона
// GroupCard/GroupsList остаются с прежними именами, без prop-дреллинга
export interface GroupsListCtx extends ReturnType<typeof useGroupsList> {
  editGroup: (g: GroupView) => void
}
export const GROUPS_LIST: InjectionKey<GroupsListCtx> = Symbol('groups-list')

// чекер скрытия выключенного (80-док, бонус): групп много, глазами
// фильтровать тяжело. ДВА РАЗДЕЛЬНЫХ тумблера (слово владельца 2026-10-21:
// «скрыть совсем не активные группы, но правила видеть, чтобы включать
// выключать») + счётчики «выключено: N» рядом. Состояние - в localStorage
// по паттерну privacy (переживает перезагрузку, не лезет в сервер).
const HIDE_OFF_G_LS = 'mawg-hideoff-groups';
const HIDE_OFF_R_LS = 'mawg-hideoff-rules';

export function useHideOffFlags() {
  const hideOffGroupsFlag = ref(false);
  const hideOffRulesFlag = ref(false);
  try {
    // недолго живший единый ключ - миграция в два раздельных
    const legacy = localStorage.getItem('mawg-hideoff');
    hideOffGroupsFlag.value = localStorage.getItem(HIDE_OFF_G_LS) === '1' || legacy === '1';
    hideOffRulesFlag.value = localStorage.getItem(HIDE_OFF_R_LS) === '1' || legacy === '1';
    if (legacy !== null) localStorage.removeItem('mawg-hideoff');
  } catch {
    // jsdom/приватный режим без localStorage - просто не переживает ребут
  }
  function setHideOffGroups(v: boolean): void {
    hideOffGroupsFlag.value = v;
    try { localStorage.setItem(HIDE_OFF_G_LS, v ? '1' : '0'); } catch { /* сессия */ }
  }
  function setHideOffRules(v: boolean): void {
    hideOffRulesFlag.value = v;
    try { localStorage.setItem(HIDE_OFF_R_LS, v ? '1' : '0'); } catch { /* сессия */ }
  }
  return { hideOffGroupsFlag, hideOffRulesFlag, setHideOffGroups, setHideOffRules };
}

// ряд создания (легаси rgName/rgColor/rgIface/rgCreate); openMt - общий с
// списком (новая группа автораскрыта)
export function useCreateGroup(openMt: Ref<Set<string>>) {
  const groups = useGroups();
  const { push } = useToast();
  const { state } = groups;
  const newName = ref('');
  const newColor = ref('#4a9eff');
  const newIface = ref('');
  const creating = ref(false);

  const createIfaceOptions = computed<UiSelectOption[]>(() => {
    const out = state.ifaces.map((i) => ({
      value: i.device,
      label: i.device + (i.title && i.title !== i.device ? ' - ' + i.title : ''),
    }));
    out.push({ value: 'blackhole', label: 'blackhole (блокировать трафик)' });
    return out;
  });
  watch(() => state.ifaces, (list) => {
    if (!newIface.value && list.length) newIface.value = list[0].device;
  }, { immediate: true });

  async function createG(): Promise<void> {
    const name = newName.value.trim();
    if (!name || !newIface.value) {
      push('Укажите имя группы и интерфейс', true);
      return;
    }
    creating.value = true;
    try {
      const g = await groups.createGroup({ name, interface: newIface.value, color: newColor.value });
      newName.value = '';
      if (g) {
        openMt.value.add(g.id); // легаси: новая группа автораскрыта
        await nextTick();
        document.querySelector(`[data-gid="${window.CSS && CSS.escape ? CSS.escape(g.id) : g.id}"]`)
          ?.scrollIntoView({ behavior: 'smooth', block: 'center' });
      }
    } catch (e) {
      push(e instanceof Error ? e.message : String(e), true);
    }
    creating.value = false;
  }
  return { newName, newColor, newIface, creating, createIfaceOptions, createG };
}

export function usePresetApply(createIfaceOptions: Ref<UiSelectOption[]>) {
  const groups = useGroups();
  const { push } = useToast();
  const { confirm } = useConfirm();
  const { state } = groups;
  const presetSel = ref('');
  const presetIface = ref('');
  const applying = ref(false);

  const presetOptions = computed<UiSelectOption[]>(() =>
    state.presets.list.map((p: Preset) => ({ value: p.id, label: p.title, count: (p.rules || []).length, hint: p.note })));

  // легаси: первый пресет предвыбран
  watch(() => state.presets.list, (list) => {
    if (!presetSel.value && list.length) presetSel.value = list[0].id;
  }, { immediate: true });

  watch(() => state.ifaces, (list) => {
    if (!presetIface.value && list.length) presetIface.value = list[0].device;
  }, { immediate: true });

  async function applyPreset(): Promise<void> {
    const p = state.presets.list.find((x) => x.id === presetSel.value) || null;
    const iface = presetIface.value;
    if (!presetSel.value || !iface) {
      push('Выберите шаблон и интерфейс', true);
      return;
    }
    const ifaceTxt = createIfaceOptions.value.find((o) => o.value === iface)?.label || iface;
    const lines = [presetRulesLine(p, presetSel.value), `Интерфейс: ${ifaceTxt}`];
    if (p && p.source) lines.push(`Источник: ${p.source.replace(/^https?:\/\//, '')}`);
    const ok = await confirm({
      title: 'Применение шаблона',
      text: lines.join('\n') + '\n\nСоздать группу?',
      okLabel: 'Создать',
    });
    if (!ok) return;
    applying.value = true;
    try {
      const n = await groups.applyPreset(presetSel.value, iface);
      push(`Группа создана, правил: ${n}`);
    } catch (e) {
      push(e instanceof Error ? e.message : String(e), true);
    }
    applying.value = false;
  }
  return { presetSel, presetIface, applying, presetOptions, applyPreset };
}

// исключения из обработки (94-док): черновик + пачка через разделители.
// Функционал выключен флагом (95-док, слово владельца 30.10): блок
// гасится плашкой, кнопки disabled; PUT бэкенд всё равно отклонит (503)
export function useExcludesDraft() {
  const groups = useGroups();
  const { push } = useToast();
  const { state: status } = useStatus();
  const excludesDisabled = computed(() => !!status.data?.excludesDisabled);
  const exclDraft = ref<string[]>([]);
  const exclNew = ref('');
  const exclSaving = ref(false);
  watch(
    () => groups.state.excludes,
    (v) => {
      exclDraft.value = [...v];
    },
    { immediate: true },
  );
  const exclDirty = computed(() => JSON.stringify(exclDraft.value) !== JSON.stringify(groups.state.excludes));
  function exclAdd(): void {
    const parts = exclNew.value
      .split(/[\s,;]+/)
      .map((x) => x.trim())
      .filter(Boolean);
    for (const p of parts) {
      if (!exclDraft.value.includes(p)) exclDraft.value.push(p);
    }
    exclNew.value = '';
  }
  function exclRemove(i: number): void {
    exclDraft.value.splice(i, 1);
  }
  async function exclSave(): Promise<void> {
    exclSaving.value = true;
    try {
      const list = await groups.saveExcludes([...exclDraft.value]);
      exclDraft.value = [...list];
      push('список исключений сохранён: ' + list.length);
    } catch (e) {
      push(e instanceof Error ? e.message : String(e), true);
      await groups.loadExcludes();
      exclDraft.value = [...groups.state.excludes];
    } finally {
      exclSaving.value = false;
    }
  }
  return { excludesDisabled, exclDraft, exclNew, exclSaving, exclDirty, exclAdd, exclRemove, exclSave };
}

// гистерезис политик (легаси polFailCycles/polRestoreCycles)
export function useCyclesForm() {
  const groups = useGroups();
  const { push } = useToast();
  const { state } = groups;
  const cycFail = ref('');
  const cycRestore = ref('');
  const cycSaving = ref(false);
  watch(() => state.cycles, (c) => {
    if (!c) return;
    cycFail.value = String(c.failCycles);
    cycRestore.value = String(c.restoreCycles);
  }, { immediate: true, deep: true });
  const cycChanged = computed(() =>
    cyclesValid(cycFail.value, cycRestore.value)
    && (+cycFail.value !== state.cycles?.failCycles || +cycRestore.value !== state.cycles?.restoreCycles));

  async function saveCycles(): Promise<void> {
    if (!cycChanged.value || cycSaving.value) return;
    cycSaving.value = true;
    try {
      const r = await groups.saveCycles(+cycFail.value, +cycRestore.value);
      push(`Гистерезис политик: переключение после ${r.failCycles} отказов, возврат после ${r.restoreCycles} здоровых циклов`);
    } catch (e) {
      push(e instanceof Error ? e.message : String(e), true);
    }
    cycSaving.value = false;
  }
  return { cycFail, cycRestore, cycSaving, cycChanged, saveCycles };
}

// фокус в ТЕКСТОВОМ поле (легаси editingRulesPanel 1512 + focusout 1555):
// перерендер не сносит ввод. Чекбоксы/кнопки фокус не держат.
function isTextInput(el: EventTarget | null): boolean {
  const t = el as HTMLElement | null;
  if (!t) return false;
  if (t.tagName === 'TEXTAREA' || t.tagName === 'SELECT') return true;
  return t.tagName === 'INPUT' && !/^(checkbox|radio|button|submit|reset)$/i.test((t as HTMLInputElement).type || 'text');
}

// список групп/правил: поиск, сокрытие выключенного, DnD порядка,
// inline-редактор, добавление/импорт/копирование правил, массовое
// включение. openMt (открытые details) передан снаружи - он общий с
// рядом создания группы.
export function useGroupsList(openMt: Ref<Set<string>>) {
  const groups = useGroups();
  const { state } = groups;
  const { push } = useToast();
  const { confirm } = useConfirm();
  const router = useRouter();

  // бейдж «устройство целиком» кликабелен, когда пул связки известен
  // (97-док): переход к карточке своего пула на «Интерфейсах»
  function devPoolOf(g: GroupView): string {
    const b = state.bindings.find((x) => x.name === g.name);
    return b ? b.pool : '';
  }
  function goPool(pool: string): void {
    void router.push({ path: '/next/interfaces', query: { pool } });
  }

  async function run(fn: () => Promise<unknown>): Promise<void> {
    try {
      await fn();
    } catch (e) {
      push(e instanceof Error ? e.message : String(e), true);
    }
  }

  const q = ref('');
  const searchQ = ref('');
  const savedOpen = ref<Set<string> | null>(null);

  const { hideOffGroupsFlag, hideOffRulesFlag, setHideOffGroups, setHideOffRules } = useHideOffFlags();

  function onDetToggle(gid: string, e: Event): void {
    const opened = (e.target as HTMLDetailsElement).open;
    if (opened) openMt.value.add(gid);
    else openMt.value.delete(gid);
  }

  watch(q, (v) => {
    const nv = v.trim().toLowerCase();
    if (nv && !searchQ.value) savedOpen.value = new Set(openMt.value); // снапшот до поиска
    searchQ.value = nv;
    if (!nv && savedOpen.value) {
      openMt.value = new Set(savedOpen.value); // очистка - ровно прежняя открытость
      savedOpen.value = null;
      return;
    }
    for (const g of state.groups) if (groupMatches(g, nv)) openMt.value.add(g.id);
  });

  const visible = computed(() => {
    const s = searchQ.value;
    const base = s ? state.groups.filter((g) => groupMatches(g, s)) : state.groups;
    return hideOffGroups(base, hideOffGroupsFlag.value);
  });

  const offGroupsCount = computed(() => offGroupsTotal(state.groups));
  const offRulesCount = computed(() => offRulesTotal(state.groups));

  // при поиске внутри группы - только совпавшие правила, кроме случая
  // «совпало имя, но ни одно правило» (легаси 1680-1684); тумблер
  // «Скрывать выключенные правила» дополнительно убирает enable=false
  function ruleRows(g: GroupView): Rule[] {
    const list = g.rules || [];
    const s = searchQ.value;
    const filtered = showAllRules(g, s) ? list : list.filter((rl) => (rl.rule || '').toLowerCase().includes(s) || (rl.name || '').toLowerCase().includes(s));
    return hideOffRules(filtered, hideOffRulesFlag.value);
  }

  // editingLock: фокус в ТЕКСТОВЫХ полях списка (легаси editingRulesPanel
  // 1512 + focusout 1555) - перерендер не сносит ввод. Только текстовый
  // ввод: тап по чекбоксу (живая приёмка на телефоне) тоже ставит фокус и
  // ДЕРЖИТ его - refresh откладывался; чекбоксы/кнопки перерендер не теряют.
  const listBox = ref<HTMLElement | null>(null);
  function onFocusin(e: FocusEvent): void {
    if (isTextInput(e.target)) groups.setEditing(1);
  }
  function onFocusout(): void {
    setTimeout(() => {
      const ae = document.activeElement as HTMLElement | null;
      const box = listBox.value;
      if (!ae || !box || !box.contains(ae) || !isTextInput(ae)) {
        void groups.setEditing(-1);
      }
    }, 0);
  }

  // DnD порядка групп и правил (легаси attachDnD 1284)
  const armed = ref(''); // «scope:id» строки, чей grip нажат
  const dragCtx = ref<{ scope: string; id: string } | null>(null);
  const dropMark = ref<{ id: string; before: boolean } | null>(null);

  function rowKey(scope: string, id: string): string {
    return scope + ':' + id;
  }
  function isDragRow(scope: string, id: string): boolean {
    // при поиске/скрытии выключенного DnD отключён: фильтрованный список
    // отправил бы частичный список id
    return !searchQ.value && !hideOffGroupsFlag.value && !hideOffRulesFlag.value && armed.value === rowKey(scope, id);
  }
  function onDragStart(e: DragEvent, scope: string, id: string): void {
    if (armed.value !== rowKey(scope, id)) {
      e.preventDefault();
      return;
    }
    dragCtx.value = { scope, id };
    try {
      e.dataTransfer?.setData('text/plain', id); // Firefox без данных не тащит
    } catch { /* jsdom */ }
    groups.setEditing(1); // drag сносит перерендером - как легаси dragEl
  }
  function onDragOver(e: DragEvent, scope: string, id: string): void {
    const d = dragCtx.value;
    if (!d || d.scope !== scope || d.id === id) return;
    e.preventDefault();
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    dropMark.value = { id, before: isBeforeHalf(rect, e.clientY) };
  }
  function onDragLeave(id: string): void {
    if (dropMark.value?.id === id) dropMark.value = null;
  }
  async function onDrop(e: DragEvent, scope: string, id: string): Promise<void> {
    const d = dragCtx.value;
    const mark = dropMark.value;
    dropMark.value = null;
    if (!d || d.scope !== scope || d.id === id || !mark || mark.id !== id) return;
    e.preventDefault();
    if (scope === 'groups') {
      const list = state.groups.map((g) => g.id);
      const from = list.indexOf(d.id);
      const to = dropIndex(from, list.indexOf(id), mark.before);
      if (to !== from) await run(() => groups.reorderGroups(reorder(list, from, to)));
    } else {
      const gid = scope.slice('rules:'.length);
      const g = state.groups.find((x) => x.id === gid);
      const list = (g?.rules || []).map((r) => r.id);
      const from = list.indexOf(d.id);
      const to = dropIndex(from, list.indexOf(id), mark.before);
      if (to !== from) await run(() => groups.reorderRules(gid, reorder(list, from, to)));
    }
  }
  function onDragEnd(): void {
    dragCtx.value = null;
    dropMark.value = null;
    armed.value = '';
    void groups.setEditing(-1);
  }

  async function delGroup(g: GroupView): Promise<void> {
    const ok = await confirm({
      title: 'Удаление группы',
      text: `Удалить группу ${g.name} со всеми правилами?`,
      okLabel: 'Удалить',
    });
    if (!ok) return;
    await run(() => groups.deleteGroup(g.id));
  }

  const er = ref<{ groupId: string; ruleId: string; type: string; value: string; name: string } | null>(null);

  function editRule(g: GroupView, rl: Rule): void {
    er.value = { groupId: g.id, ruleId: rl.id, type: rl.type, value: rl.rule, name: rl.name || '' };
    groups.setEditing(1);
  }
  async function saveRule(): Promise<void> {
    const e0 = er.value;
    if (!e0) return;
    const value = e0.value.trim();
    if (!value) {
      push('Введите значение правила', true);
      return;
    }
    try {
      await groups.updateRule(e0.groupId, e0.ruleId, {
        type: e0.type,
        rule: normRuleValue(e0.type, value),
        name: e0.name.trim(),
      });
      er.value = null;
      await groups.setEditing(-1);
    } catch (e) {
      push(e instanceof Error ? e.message : String(e), true);
    }
  }
  async function cancelRule(): Promise<void> {
    er.value = null;
    await groups.setEditing(-1);
  }

  const nrType = reactive<Record<string, string>>({});
  const nrValue = reactive<Record<string, string>>({});
  const nrBusy = ref('');
  function nrTypeOf(gid: string): string {
    return nrType[gid] || 'namespace';
  }
  async function addRule(g: GroupView): Promise<void> {
    const value = (nrValue[g.id] || '').trim();
    if (!value) {
      push('Введите значение правила', true);
      return;
    }
    nrBusy.value = g.id;
    try {
      await groups.addRule(g.id, { type: nrTypeOf(g.id), rule: normRuleValue(nrTypeOf(g.id), value) });
      nrValue[g.id] = '';
      openMt.value.add(g.id);
      push('Правило добавлено');
    } catch (e) {
      push(e instanceof Error ? e.message : String(e), true);
    }
    nrBusy.value = '';
  }

  const imOpen = ref(false);
  const imG = ref<GroupView | null>(null);
  const imType = ref('auto');
  const imSecond = ref(false);
  const imEnable = ref(true);
  const imText = ref('');
  const imBusy = ref(false);

  function openImport(g: GroupView): void {
    imG.value = g;
    imText.value = '';
    imOpen.value = true;
  }
  async function doImport(): Promise<void> {
    const g = imG.value;
    if (!g) return;
    if (!imText.value.trim()) {
      push('Вставьте список', true);
      return;
    }
    imBusy.value = true;
    try {
      const r = await groups.importRules(g.id, {
        text: imText.value,
        type: imType.value,
        toSecond: imSecond.value,
        enable: imEnable.value,
      });
      imOpen.value = false;
      const badNote = r.bad && r.bad.length ? ' Отброшено: ' + r.bad.join('; ') : '';
      push(r.added
        ? `Импортировано: ${r.added}, пропущено: ${r.skipped}.${badNote}`
        : 'Нового ничего не добавлено, все дубликаты' + badNote);
    } catch (e) {
      push(e instanceof Error ? e.message : String(e), true);
    }
    imBusy.value = false;
  }

  const cpOpen = ref(false);
  const cpText = ref('');
  const cpTitle = ref('');

  async function copyRules(g: GroupView): Promise<void> {
    const lines = (g.rules || []).map((r) => r.rule);
    if (!lines.length) {
      push('В группе нет правил', true);
      return;
    }
    const text = lines.join('\n');
    if (navigator.clipboard && window.isSecureContext) {
      try {
        await navigator.clipboard.writeText(text);
        push(`Скопировано: ${lines.length}`);
        return;
      } catch { /* фолбэк в диалог */ }
    }
    cpTitle.value = `Список правил (${lines.length})`;
    cpText.value = text;
    cpOpen.value = true;
  }

  const bulkBusy = ref('');
  async function bulkRules(g: GroupView, enable: boolean): Promise<void> {
    const total = (g.rules || []).length;
    if (!total) return;
    const ok = await confirm({
      title: enable ? 'Включить все правила' : 'Выключить все правила',
      text: `Все правила группы «${g.name}» (${total} шт.) будут ${enable ? 'включены' : 'выключены'}.`,
      okLabel: enable ? 'Включить все' : 'Выключить все',
    });
    if (!ok) return;
    bulkBusy.value = g.id + (enable ? ':on' : ':off');
    try {
      const r = await groups.enableAllRules(g.id, enable);
      push(`Группа «${g.name}»: ${enable ? 'включено' : 'выключено'} правил ${r.changed} из ${r.total}`);
    } catch (e) {
      push(e instanceof Error ? e.message : String(e), true);
    }
    bulkBusy.value = '';
  }

  return {
    q, searchQ, openMt,
    visible, offGroupsCount, offRulesCount,
    hideOffGroupsFlag, hideOffRulesFlag, setHideOffGroups, setHideOffRules,
    listBox, onFocusin, onFocusout,
    armed, dragCtx, dropMark, rowKey, isDragRow,
    onDragStart, onDragOver, onDragLeave, onDrop, onDragEnd,
    devPoolOf, goPool, run,
    delGroup, bulkBusy, bulkRules,
    er, editRule, saveRule, cancelRule,
    nrType, nrValue, nrBusy, nrTypeOf, addRule,
    imOpen, imG, imType, imSecond, imEnable, imText, imBusy, openImport, doImport,
    cpOpen, cpText, cpTitle, copyRules,
    ruleRows, onDetToggle,
  };
}
