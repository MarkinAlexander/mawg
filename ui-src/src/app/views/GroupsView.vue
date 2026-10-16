<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import {
  UiBadge, UiBanner, UiButton, UiCard, UiColor, UiDialog, UiHelp, UiHint, UiIcon,
  UiInput, UiSelect, type UiSelectOption, UiSwitch, UiTextarea,
} from '../../components/ui'
import { dropIndex, isBeforeHalf, reorder } from '../../components/ui/dnd'
import { useConfirm } from '../../stores/confirm'
import { useGroups } from '../../stores/groups'
import { useToast } from '../../stores/toast'
import { G } from '../glossary'
import type { GroupView, Preset, Rule } from '../../api/types'
import {
  cyclesValid, degradedText, dupsEntries, groupHasDup, groupMatchCount,
  groupMatches, hlSegs, IMPORT_TYPE_OPTIONS, normRuleValue, POLICY_SHORT,
  presetRulesLine, RULE_HINTS, RULE_PLACEHOLDERS, RULE_TYPES, ruleTypeOptions,
  showAllRules, srcBadge, warnTitle,
} from './groupsLogic'
import CascadeDialog from './CascadeDialog.vue'
import GroupDialog from './GroupDialog.vue'
import { useSecrets } from '../secrets'

// Фаза 3.7 «Группы правил» (легаси #rulescard, renderRules 1591).
// 3.7а: список+поиск+правила+редактор базы; DnD групп и правил - порт
// attachDnD (panel.js 1284) на хелперах dnd.ts; inline-правка правила и
// фокус в полях списка держат editingLock стора (легаси editingRulesPanel
// 1512) - перерендер откладывается до выхода. 3.7б: пресеты (ряд
// «шаблон на интерфейс», легаси 2148-2259) и каскады (CascadeDialog,
// легаси 2050-2107). Бэкапы и импорт/выгрузка файлом - Ф3.7в.

const groups = useGroups();
const { state } = groups;
const { push } = useToast();
const { confirm } = useConfirm();
const route = useRoute();
// приватный режим: в правилах скрываются только адреса из конфигов
// (hosts каскадов), домены и чужие подсети видны (слово владельца)
const secrets = useSecrets();

onMounted(() => {
  groups.start(30000); // легаси: setInterval(refreshRules, 30000)
  void groups.loadPresets(); // легаси loadPresets() на старте, без поллинга
});
onBeforeUnmount(() => groups.stop());

function enabledRulesCount(g: GroupView): number {
  return (g.rules || []).filter((r) => r.enable).length;
}
function disabledRulesCount(g: GroupView): number {
  return (g.rules || []).length - enabledRulesCount(g);
}

async function run(fn: () => Promise<unknown>): Promise<void> {
  try {
    await fn();
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
}

// --- создание группы (легаси rgCreate 2109) ---
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

// --- пресеты (легаси 2148-2259) ---
const presetSel = ref('');
const presetIface = ref('');
const applying = ref(false);

const presetOptions = computed<UiSelectOption[]>(() =>
  state.presets.list.map((p: Preset) => ({ value: p.id, label: p.title, count: (p.rules || []).length })));

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

// --- каскад (легаси 2050-2107) ---
const cascadeOpen = ref(false);

// --- гистерезис политик (легаси 1213-1246) ---
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

// --- поиск (легаси ruleSearch 1733 + renderRules 1591) ---
const q = ref('');
const searchQ = ref('');
const savedOpen = ref<Set<string> | null>(null);
const openMt = ref<Set<string>>(new Set()); // открытые details, переживают перерендер

// открытость details в openMt (легаси det.ontoggle 1673)
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
  return s ? state.groups.filter((g) => groupMatches(g, s)) : state.groups;
});

// при поиске внутри группы - только совпавшие правила, кроме случая
// «совпало имя, но ни одно правило» (легаси 1680-1684)
function ruleRows(g: GroupView): Rule[] {
  const list = g.rules || [];
  const s = searchQ.value;
  if (showAllRules(g, s)) return list;
  return list.filter((rl) => (rl.rule || '').toLowerCase().includes(s) || (rl.name || '').toLowerCase().includes(s));
}

// --- editingLock: фокус в ТЕКСТОВЫХ полях списка (легаси
// editingRulesPanel 1512 + focusout 1555): перерендер не сносит ввод.
// Живая приёмка на телефоне: тумблер группы - тоже INPUT(checkbox), тап
// по нему ставит фокус и ДЕРЖИТ его - editingLock не отпускался,
// refresh откладывался и строки правил при открытой группе не
// перерисовывались. Лочим только текстовый ввод: чекбоксы/кнопки
// перерендер не теряют.
const listBox = ref<HTMLElement | null>(null);
function isTextInput(el: EventTarget | null): boolean {
  const t = el as HTMLElement | null;
  if (!t) return false;
  if (t.tagName === 'TEXTAREA' || t.tagName === 'SELECT') return true;
  return t.tagName === 'INPUT' && !/^(checkbox|radio|button|submit|reset)$/i.test((t as HTMLInputElement).type || 'text');
}
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

// --- DnD порядка групп и правил (легаси attachDnD 1284; при поиске
// отключён - фильтрованный список отправил бы частичный список id) ---
const armed = ref(''); // «scope:id» строки, чей grip нажат
const dragCtx = ref<{ scope: string; id: string } | null>(null);
const dropMark = ref<{ id: string; before: boolean } | null>(null);

function rowKey(scope: string, id: string): string {
  return scope + ':' + id;
}
function isDragRow(scope: string, id: string): boolean {
  return !searchQ.value && armed.value === rowKey(scope, id);
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

// --- мутации строк (легаси 1665-1671) ---
async function delGroup(g: GroupView): Promise<void> {
  const ok = await confirm({
    title: 'Удаление группы',
    text: `Удалить группу ${g.name} со всеми правилами?`,
    okLabel: 'Удалить',
  });
  if (!ok) return;
  await run(() => groups.deleteGroup(g.id));
}

// --- inline-редактор правила (легаси editRule 1325) ---
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

// --- добавление правила (легаси nr-add 1713) ---
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

// --- импорт правил текстом (легаси dlgImport 1440-1470) ---
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

// --- копирование правил (легаси copyGroupRules 1472) ---
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

// --- совпадения в группах (легаси showMTDupsDlg 1752) ---
const dupsOpen = ref(false);
const dupsList = computed(() => dupsEntries(state.groups, state.duplicates, searchQ.value));

// --- резервные копии: кнопка ведёт на общую страницу (52/53-доки) ---
const router = useRouter();

// --- диалог группы ---
const editOpen = ref(false);
const editing = ref<GroupView | null>(null);
function editGroup(g: GroupView): void {
  editing.value = g;
  editOpen.value = true;
}

// --- массовое включение/выключение правил группы (слово владельца) ---
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

// deep-link (44-дока §6): /next/groups?g=<id> открывает редактор группы,
// как только список загрузился (или сразу, если стор уже жив); запрос
// тут же чистится, чтобы F5 не переоткрывал редактор
function openFromLink(): void {
  if (!state.loaded) return;
  const gid = route.query.g;
  if (typeof gid === 'string' && gid) {
    const g = state.groups.find((x) => x.id === gid);
    if (g) editGroup(g);
    router.replace({ query: { ...route.query, g: undefined } });
  }
}
watch(() => route.query.g, openFromLink);
onMounted(openFromLink);
</script>

<template>
  <!-- id - якорь для поиска по настройкам (SearchDialog, search.ts) -->
  <UiCard
    id="card-rules"
    title="Правила маршрутизации"
    subtitle="какой трафик идёт через VPN, а какой - напрямую: правила по доменам, подсетям и отдельным IP."
  >
    <template #actions>
      <UiHelp :text="G.group + ' ' + G.ruleTypes" />
      <UiButton
        tooltip="одинаковые правила в разных группах; с поиском - все группы, где встречается искомое"
        @click="dupsOpen = true"
      >
        показать совпадения в группах
      </UiButton>
      <!-- слово владельца: кнопка ведёт на общую страницу резервных копий
           (там раскрыто - полный бэкап, восстановление, импорт/выгрузка) -->
      <UiButton
        tooltip="страница резервных копий: полный бэкап панели, восстановление, импорт и выгрузка файлом"
        @click="router.push('/next/backups')"
      >
        Резервные копии
      </UiButton>
    </template>

    <!-- ряд создания (легаси rgName/rgColor/rgIface/rgCreate) - одна линия,
         контролы без подписей (легаси-placeholder'ы) -->
    <div class="crow">
      <UiInput
        v-model="newName"
        bare
        placeholder="имя группы"
        class="cname"
      />
      <UiColor
        v-model="newColor"
        bare
        tooltip="цвет группы"
        class="ccolor"
      />
      <UiSelect
        v-model="newIface"
        :options="createIfaceOptions"
        bare
        class="ciface"
      />
      <UiButton
        variant="primary"
        :busy="creating"
        @click="createG"
      >
        + группа
      </UiButton>
    </div>

    <!-- каскад - отдельно, с точным описанием (слово владельца: внимательнее) -->
    <div class="cascdesc">
      <div class="ctext">
        <b>Каскад</b> - направить трафик одного подключения через другое: создаётся
        служебная группа (в списке - с бейджем «каскад»). Адреса серверов источника
        подставляются сами и обновляются при ротации. Включается и выключается
        обычным переключателем группы.<UiHelp :text="G.cascade" />
      </div>
      <UiButton
        tooltip="направить трафик одного подключения через другое"
        @click="cascadeOpen = true"
      >
        + каскад
      </UiButton>
    </div>

    <!-- ряд пресетов (легаси presetSel/presetIface/presetApply/presetSrc) -->
    <div class="crow psetrow">
      <UiSelect
        v-model="presetSel"
        :options="presetOptions"
        bare
        class="psel"
      />
      <UiSelect
        v-model="presetIface"
        :options="createIfaceOptions"
        bare
      />
      <UiButton
        :busy="applying"
        @click="applyPreset"
      >
        шаблон на интерфейс
      </UiButton>
    </div>
    <div
      v-if="state.presets.sourceUrl"
      class="muted psetsrc"
    >
      Списки: <a
        :href="state.presets.sourceUrl"
        target="_blank"
        rel="noopener"
      >github.com/itdoginfo/allow-domains</a>{{ state.presets.sourceNote ? ' (' + state.presets.sourceNote + ')' : '' }}. Более точные и свежие списки - в репозитории источника.
    </div>

    <!-- гистерезис политик (легаси polFailCycles/polRestoreCycles) -
         предложение в одну линию, как в легаси -->
    <div class="cycles">
      <span class="muted">Канал пропал: переводить группы на запасной после</span>
      <UiInput
        v-model="cycFail"
        type="number"
        bare
        class="cnum"
        placeholder="2"
        tooltip="сколько циклов проверки (30с) интерфейс должен считаться мёртвым подряд"
      />
      <span class="muted">циклов отказа, возвращать после</span>
      <UiInput
        v-model="cycRestore"
        type="number"
        bare
        class="cnum"
        placeholder="2"
        tooltip="сколько циклов подряд интерфейс должен быть живым перед возвратом"
      />
      <span class="muted">здоровых циклов</span>
      <UiButton
        :disabled="!cycChanged"
        :busy="cycSaving"
        @click="saveCycles"
      >
        сохранить
      </UiButton>
      <UiHelp :text="G.hysteresis" />
    </div>
    <UiHint
      class="cyshint"
      text="Цикл проверки - каждые 30 секунд. Меньше 2 не рекомендуется: одна потерянная проба будет уводить группы на запасной, а флапающий канал - переключать их каждые 30-60 секунд и рвать соединения. 2/2 - разумный компромисс: реакция ~1-1.5 минуты, возврат только после устойчивого восстановления."
    />

    <!-- баннеры движка MT (легаси updateMTBanners 1559) -->
    <UiBanner
      v-if="state.glitchy"
      kind="err"
      text="Обнаружена глючная сборка MagiTrickle: массовые записи усекают группы. Поставьте наш форк с фиксами -"
      :link="{ href: 'https://gitlab.com/magitrickle/magitrickle/-/merge_requests/222', label: 'MR !222' }"
      class="mtnote"
    />
    <UiBanner
      v-if="state.wrapper"
      kind="warn"
      text="В mangle OUTPUT видна обёртка юзера (MR_*): она выводит группы в OUTPUT сама, каскадные хуки mawg не ставятся, чтобы не дублировать обработку."
      class="mtnote"
    />
    <UiBanner
      v-if="!state.sourcesCapable"
      kind="warn"
      text="Подключённый MagiTrickle не поддерживает источники групп: режимы «кто ходит в группу» спрятаны. Поставьте форк 0.8.2-rev3 или новее."
      class="mtnote"
    />

    <!-- поиск (легаси ruleSearch) -->
    <div class="srow">
      <UiInput
        v-model="q"
        label="поиск"
        placeholder="поиск: показываются только группы с совпадениями"
        class="sinput"
      />
    </div>

    <!-- список групп (легаси renderRules 1591) -->
    <div
      ref="listBox"
      class="mtlist"
      @focusin="onFocusin"
      @focusout="onFocusout"
    >
      <UiHint
        v-if="!state.loaded"
        text="загружаю группы..."
      />
      <UiHint
        v-else-if="!state.available"
        text="MagiTrickle недоступен"
      />
      <UiHint
        v-else-if="searchQ && !visible.length"
        :text="`Ничего не найдено по «${searchQ}».`"
      />
      <UiHint
        v-else-if="!state.groups.length"
        text="Групп нет. Создайте группу или примените шаблон."
      />

      <div
        v-for="g in visible"
        :key="g.id"
        :data-gid="g.id"
        class="mtgroup"
        :class="{ block: g.interface === 'blackhole', dragging: dragCtx?.scope === 'groups' && dragCtx?.id === g.id, 'drop-before': dropMark?.id === g.id && dropMark?.before === true, 'drop-after': dropMark?.id === g.id && dropMark?.before === false }"
        :style="{ '--gcol': g.color || '#4a9eff' }"
        :draggable="isDragRow('groups', g.id)"
        @dragstart="onDragStart($event, 'groups', g.id)"
        @dragover="onDragOver($event, 'groups', g.id)"
        @dragleave="onDragLeave(g.id)"
        @drop="onDrop($event, 'groups', g.id)"
        @dragend="onDragEnd"
      >
        <div class="grow">
          <span
            class="grip"
            title="перетащите, чтобы изменить порядок"
            @pointerdown="armed = rowKey('groups', g.id)"
            @pointerup="armed = ''"
          ><UiIcon name="grip" /></span>
          <span
            class="gcolor"
            :style="{ background: g.color || '#4a9eff' }"
          />
          <b class="gname"><template
            v-for="(s, i) in hlSegs(g.name, searchQ)"
            :key="i"
          ><mark v-if="s.hit">{{ s.text }}</mark><template v-else>{{ s.text }}</template></template></b>
          <span
            v-if="groupHasDup(g, state.duplicates)"
            class="warnb"
            title="в группе есть правила, дублирующиеся в других группах"
          ><UiIcon name="warn" /></span>
          <UiBadge
            v-if="g.cascade"
            text="каскад"
            variant="muted"
            :tooltip="G.cascade"
          />
          <UiBadge
            v-if="g.interface === 'blackhole'"
            text="блокировка"
            variant="err"
            :tooltip="G.blackhole"
          />
          <UiBadge
            v-if="g.policy && g.policy.onDead"
            :text="`если канал пропал: ${POLICY_SHORT[g.policy.onDead] || g.policy.onDead}`"
            variant="warn"
            :tooltip="G.hysteresis"
          />
          <UiBadge
            v-if="srcBadge(g)"
            :text="srcBadge(g)!.text"
            :tooltip="srcBadge(g)!.tooltip"
            variant="ok"
          />
          <span class="ifaceb">
            <UiBadge :text="g.interface" />
            <span
              v-if="g.interfaceTitle"
              class="iftitle"
            >{{ g.interfaceTitle }}</span>
          </span>
          <!-- слово владельца: формат «всего/включено», без чипа - чипы
               только для важных вещей -->
          <span
            class="muted rcount"
            title="правил всего/включено"
          >правил: {{ (g.rules || []).length }}/{{ enabledRulesCount(g) }}<template v-if="searchQ"> · <span class="okc">совпадений: {{ groupMatchCount(g, searchQ) }}</span></template></span>
          <span class="sp" />
          <!-- импорт/копирование правил группы - иконки в шапке (слово
               владельца 2026-10-14: из ряда правил убрать, svg даст позже) -->
          <UiButton
            variant="icon"
            tooltip="вставить список доменов, подсетей или ссылок"
            @click="openImport(g)"
          >
            <UiIcon name="plus-circle" />
          </UiButton>
          <UiButton
            variant="icon"
            tooltip="скопировать все правила группы списком"
            @click="copyRules(g)"
          >
            <UiIcon name="copy" />
          </UiButton>
          <UiSwitch
            :model-value="g.enable"
            :tooltip="g.enable ? 'выключить группу' : 'включить группу'"
            @update:model-value="(v: boolean) => run(() => groups.toggleGroup(g.id, v))"
          />
          <UiButton
            variant="icon"
            tooltip="изменить группу"
            @click="editGroup(g)"
          >
            <UiIcon name="pencil" />
          </UiButton>
          <UiButton
            variant="icon"
            danger-icon
            tooltip="удалить группу"
            @click="delGroup(g)"
          >
            <UiIcon name="trash" />
          </UiButton>
          <div
            v-if="degradedText(g)"
            class="deg muted"
          >
            {{ degradedText(g) }}
          </div>
        </div>
        <details
          :open="openMt.has(g.id)"
          @toggle="onDetToggle(g.id, $event)"
        >
          <summary>правила</summary>
          <div
            v-if="!g.enable"
            class="muted offnote"
          >
            группа выключена - правила сейчас не работают; тумблеры показывают их собственное состояние
          </div>
          <!-- слово владельца: массовое включение/выключение правил (465
               штук по одному не нащёлкаешь); один PUT группы на бэкенде -->
          <div class="bulkrow">
            <UiButton
              :busy="bulkBusy === g.id + ':on'"
              :disabled="disabledRulesCount(g) === 0 && (g.rules || []).length > 0"
              tooltip="включить все правила группы одним действием"
              @click="bulkRules(g, true)"
            >
              включить все ({{ enabledRulesCount(g) }}/{{ (g.rules || []).length }})
            </UiButton>
            <UiButton
              :busy="bulkBusy === g.id + ':off'"
              :disabled="enabledRulesCount(g) === 0"
              tooltip="выключить все правила группы одним действием"
              @click="bulkRules(g, false)"
            >
              выключить все
            </UiButton>
          </div>
          <div class="nrrow">
            <UiSelect
              :model-value="nrTypeOf(g.id)"
              :options="ruleTypeOptions()"
              bare
              class="nrtype"
              @update:model-value="(v: string) => { nrType[g.id] = v }"
            />
            <UiInput
              :model-value="nrValue[g.id] || ''"
              bare
              class="nrval"
              :placeholder="RULE_PLACEHOLDERS[nrTypeOf(g.id)]"
              @update:model-value="(v: string) => { nrValue[g.id] = v }"
            >
              <template #action>
                <UiButton
                  :busy="nrBusy === g.id"
                  @click="addRule(g)"
                >
                  + правило
                </UiButton>
              </template>
            </UiInput>
          </div>
          <div class="muted nrhint">
            {{ RULE_HINTS[nrTypeOf(g.id)] }}
          </div>
          <table class="rules">
            <thead>
              <tr>
                <th class="gripcol" />
                <th>тип</th>
                <th>значение</th>
                <th />
              </tr>
            </thead>
            <tbody>
              <template
                v-for="rl in ruleRows(g)"
                :key="rl.id"
              >
                <!-- inline-редактор вместо строки (легаси editRule 1325) -->
                <tr
                  v-if="er && er.groupId === g.id && er.ruleId === rl.id"
                  class="erow"
                >
                  <td :colspan="4">
                    <div class="erwrap">
                      <UiSelect
                        v-model="er.type"
                        :options="ruleTypeOptions()"
                        bare
                        class="ertype"
                      />
                      <UiInput
                        v-model="er.value"
                        bare
                        class="erval"
                      >
                        <template #action>
                          <UiButton
                            variant="primary"
                            @click="saveRule"
                          >
                            сохранить
                          </UiButton>
                          <UiButton @click="cancelRule">
                            отмена
                          </UiButton>
                        </template>
                      </UiInput>
                      <UiInput
                        v-model="er.name"
                        bare
                        class="ername"
                        placeholder="имя (необяз.)"
                      />
                    </div>
                    <div class="muted erhiant">
                      {{ RULE_HINTS[er.type] || '' }}
                    </div>
                  </td>
                </tr>
                <tr
                  v-else
                  :class="{ dimmed: !rl.enable || !g.enable, dragging: dragCtx?.scope === 'rules:' + g.id && dragCtx?.id === rl.id, 'drop-before': dropMark?.id === rl.id && dropMark?.before === true, 'drop-after': dropMark?.id === rl.id && dropMark?.before === false }"
                  :draggable="isDragRow('rules:' + g.id, rl.id)"
                  @dragstart="onDragStart($event, 'rules:' + g.id, rl.id)"
                  @dragover="onDragOver($event, 'rules:' + g.id, rl.id)"
                  @dragleave="onDragLeave(rl.id)"
                  @drop="onDrop($event, 'rules:' + g.id, rl.id)"
                  @dragend="onDragEnd"
                >
                  <td class="gripcol">
                    <span
                      class="grip"
                      title="перетащите, чтобы изменить порядок"
                      @pointerdown="armed = rowKey('rules:' + g.id, rl.id)"
                      @pointerup="armed = ''"
                    ><UiIcon name="grip" /></span>
                  </td>
                  <td>
                    <UiBadge
                      :text="RULE_TYPES[rl.type] || rl.type"
                      variant="muted"
                    />
                  </td>
                  <!-- secret по значению: адрес/подсеть из конфигов (hosts
                       каскадов); домены и 1.1.1.1 видны (слово владельца) -->
                  <td
                    class="rval"
                    :class="{ secret: secrets.rule(rl.rule) }"
                  >
                    <template
                      v-for="(s, i) in hlSegs(rl.rule, searchQ)"
                      :key="i"
                    >
                      <mark v-if="s.hit">{{ s.text }}</mark><template v-else>
                        {{ s.text }}
                      </template>
                    </template><span
                      v-if="rl.enable && (state.duplicates[(rl.rule || '').toLowerCase()] || []).length"
                      class="warnb"
                      :title="warnTitle(state.duplicates[(rl.rule || '').toLowerCase()] || [])"
                    ><UiIcon name="warn" /></span><span
                      v-if="rl.name"
                      class="muted"
                    > (<template
                      v-for="(s, i) in hlSegs(rl.name, searchQ)"
                      :key="i"
                    ><mark v-if="s.hit">{{ s.text }}</mark><template v-else>{{ s.text }}</template></template>)</span>
                  </td>
                  <td class="ract">
                    <UiSwitch
                      :model-value="rl.enable"
                      :tooltip="rl.enable ? 'выключить правило' : 'включить правило'"
                      @update:model-value="(v: boolean) => run(() => groups.toggleRule(g.id, rl.id, v))"
                    />
                    <UiButton
                      variant="icon"
                      tooltip="изменить правило"
                      @click="editRule(g, rl)"
                    >
                      <UiIcon name="pencil" />
                    </UiButton>
                    <UiButton
                      variant="icon"
                      danger-icon
                      tooltip="удалить правило"
                      @click="run(() => groups.deleteRule(g.id, rl.id))"
                    >
                      <UiIcon name="trash" />
                    </UiButton>
                  </td>
                </tr>
              </template>
              <tr v-if="!ruleRows(g).length">
                <td
                  colspan="4"
                  class="muted"
                >
                  правил нет
                </td>
              </tr>
            </tbody>
          </table>
        </details>
      </div>
    </div>

    <!-- импорт правил текстом (легаси dlgImport) -->
    <UiDialog
      v-model:open="imOpen"
      :title="imG ? 'Импорт правил в ' + imG.name : 'Импорт правил'"
      :busy="imBusy"
      width="520px"
    >
      <div class="hrow">
        <UiSelect
          v-model="imType"
          :options="IMPORT_TYPE_OPTIONS"
          label="Тип для доменов"
        />
        <UiHelp :text="G.cidr" />
      </div>
      <UiHint text="Ссылки всегда очищаются до домена: https://www.ozon.ru/path -> www.ozon.ru, ftp://files.example.com/dir -> files.example.com" />
      <UiSwitch
        v-model="imSecond"
        label="Усекать до второго уровня"
        hint="www.ozon.ru -> ozon.ru, зоны вроде bbc.co.uk не трогаются"
      />
      <UiSwitch
        v-model="imEnable"
        label="Импортировать включёнными"
        hint="выключенное правило можно включить позже переключателем"
      />
      <UiTextarea
        v-model="imText"
        label="Список: по одному в строке, пробелы/запятые/точки с запятой тоже разделяют"
        :rows="10"
        mono
      />
      <UiHint text="Дубликаты внутри списка и совпадения с существующими правилами пропускаются. Кириллические домены вставляйте в punycode (латинская запись вида xn--...)." />
      <template #footer>
        <UiButton
          :disabled="imBusy"
          @click="imOpen = false"
        >
          отмена
        </UiButton>
        <UiButton
          variant="primary"
          :busy="imBusy"
          @click="doImport"
        >
          импортировать
        </UiButton>
      </template>
    </UiDialog>

    <!-- фолбэк копирования (легаси dlgCopy) -->
    <UiDialog
      v-model:open="cpOpen"
      :title="cpTitle"
      width="520px"
    >
      <textarea
        :value="cpText"
        readonly
        rows="12"
        class="cptext"
        spellcheck="false"
      />
      <UiHint text="Если не скопировалось само - выделено, нажмите Ctrl+C." />
      <template #footer>
        <UiButton
          variant="primary"
          @click="cpOpen = false"
        >
          Закрыть
        </UiButton>
      </template>
    </UiDialog>

    <!-- совпадения в группах (легаси dlgMTDups) -->
    <UiDialog
      v-model:open="dupsOpen"
      title="Совпадения в группах"
      width="520px"
    >
      <template v-if="dupsList.length">
        <div
          v-for="e in dupsList"
          :key="e.pat"
          class="duprow"
        >
          <div class="duppat">
            <b>{{ e.pat }}</b>
            <span class="muted"> ({{ e.types }})</span>
            <UiBadge
              v-if="e.dup"
              :text="`дубликат: ${e.groups.length} группы`"
              variant="warn"
            />
          </div>
          <div class="muted dupgroups">
            группы: {{ e.groups.join(', ') }}
          </div>
        </div>
      </template>
      <UiHint
        v-else
        :text="searchQ ? `Ничего не найдено по «${searchQ}».` : 'Межгрупповых дубликатов нет.'"
      />
      <template #footer>
        <UiButton
          variant="primary"
          @click="dupsOpen = false"
        >
          Закрыть
        </UiButton>
      </template>
    </UiDialog>

    <GroupDialog
      v-model:open="editOpen"
      :group="editing"
    />

    <CascadeDialog v-model:open="cascadeOpen" />
  </UiCard>
</template>

<style scoped>
.hrow {
  display: flex;
  gap: 6px;
  align-items: flex-start;
  margin-top: 4px;
}
.hrow > :first-child {
  flex: 1;
  min-width: 0;
}
.bulkrow {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  justify-content: flex-end;
  margin: 6px 0 2px;
}
.crow {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
/* bare-контролы кита несут margin-top: 3px под лейбл - в инлайн-рядах он
   не нужен, вся линия по центру */
.crow :deep(.inline),
.cycles :deep(.inline),
.nrrow :deep(.inline),
.erwrap :deep(.inline) {
  margin-top: 0;
}
.psetrow {
  margin-top: 8px;
}
.psel {
  flex: 1;
  min-width: 240px;
}
.psetsrc {
  font-size: 11px;
  margin-top: 4px;
}
.cname {
  flex: 1;
  min-width: 180px;
}
.ciface {
  width: 220px;
}
.cascdesc {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 8px;
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: 8px;
}
.ctext {
  flex: 1;
  color: var(--dim);
  font-size: 12px;
}
.ctext b {
  color: var(--text);
}
.cycles {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 12px;
  font-size: 12px;
}
.cnum {
  width: 64px;
}
.cyshint {
  margin-top: 4px;
}
.mtnote {
  margin-top: 6px;
}
.srow {
  display: flex;
  margin-top: 10px;
}
.sinput {
  flex: 1;
  min-width: 200px;
}
.mtlist {
  margin-top: 10px;
}
.mtgroup {
  border: 1px solid var(--border);
  border-left: 3px solid var(--gcol);
  border-radius: 8px;
  padding: 8px 10px;
  margin-bottom: 8px;
  background: var(--bg-panel, transparent);
}
.mtgroup.block {
  border-left-color: var(--err);
}
.grow {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.grip {
  color: var(--dim);
  cursor: grab;
  display: inline-flex;
  padding: 2px;
}
.grip:active {
  cursor: grabbing;
}
.gcolor {
  width: 14px;
  height: 14px;
  border-radius: 3px;
  flex: none;
}
.gname {
  word-break: break-all;
}
.warnb {
  color: var(--warn);
  display: inline-flex;
  cursor: help;
}
.ifaceb {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.iftitle {
  color: var(--dim);
  font-size: 11px;
}
.rcount {
  font-size: 12px;
}
.okc {
  color: var(--ok);
}
.sp {
  flex: 1;
}
.deg {
  flex-basis: 100%;
  font-size: 11px;
}
details {
  margin-top: 6px;
}
summary {
  cursor: pointer;
  color: var(--dim);
  font-size: 12px;
  user-select: none;
}
.offnote {
  font-size: 11px;
  margin: 2px 0 6px;
}
.nrrow {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-top: 6px;
}
.nrtype {
  width: 190px;
}
.nrval {
  flex: 1;
  min-width: 180px;
}
.nrhint {
  font-size: 11px;
  margin-top: 2px;
}
.rules {
  width: 100%;
  border-collapse: collapse;
  font-size: 12.5px;
  margin-top: 8px;
}
.rules th {
  text-align: left;
  color: var(--dim);
  font-weight: normal;
  padding: 4px 6px;
  border-bottom: 1px solid var(--border);
}
.rules td {
  padding: 4px 6px;
  border-bottom: 1px solid var(--border);
  word-break: break-all;
}
.gripcol {
  width: 18px;
}
.rval mark,
.gname mark {
  background: rgba(217, 160, 50, 0.35); /* легаси mark: подсветка поиска */
  color: inherit;
  padding: 0 2px;
  border-radius: 3px;
}
.ract {
  white-space: nowrap;
  display: flex;
  align-items: center;
  gap: 8px;
  justify-content: flex-end;
}
tr.dimmed td,
.dimmed {
  opacity: 0.45;
}
tr.dragging {
  opacity: 0.4;
}
.drop-before {
  box-shadow: inset 0 2px 0 var(--accent);
}
tr.drop-before td {
  border-top: 2px solid var(--accent);
}
tr.drop-after td {
  border-bottom: 2px solid var(--accent);
}
.mtgroup.drop-before {
  border-top: 2px solid var(--accent);
}
.mtgroup.drop-after {
  border-bottom: 2px solid var(--accent);
}
.erwrap {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  align-items: center;
}
.ertype {
  width: 170px;
}
.erval {
  flex: 1;
  min-width: 180px;
}
.ername {
  width: 160px;
}
.erhiant {
  font-size: 11px;
  margin-top: 2px;
}
@media (max-width: 820px) {
  /* слово владельца: таблица правил на телефоне ломалась в «змейку».
     DOM прежний (DnD/inline-редактор/editingLock не тронуты) - меняется
     только раскладка: строка-карточка, значение на всю ширину */
  .rules {
    display: block;
    border-collapse: separate;
    border-spacing: 0;
  }
  .rules thead {
    display: none;
  }
  .rules tbody {
    display: block;
  }
  .rules tr {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 2px 6px;
    background: var(--panel2);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 6px;
    margin: 0 0 6px;
  }
  .rules td {
    display: inline-flex;
    align-items: center;
    padding: 0;
    border-bottom: none;
  }
  .rules td.gripcol {
    width: auto;
    order: 1;
  }
  .rules td:nth-child(2) {
    order: 2;
  }
  .rules td.ract {
    order: 3;
    margin-left: auto;
  }
  .rules td.rval {
    order: 4;
    flex: 1 1 100%;
  }
  /* inline-редактор на мобиле - блоком, поля в столбик на всю ширину */
  .rules tr.erow {
    display: block;
  }
  .rules tr.erow > td {
    display: block;
  }
  .erwrap {
    flex-direction: column;
    align-items: stretch;
  }
  .ertype,
  .ername {
    width: 100%;
  }
  /* границы drop-индикации - на саму карточку (td-границы на flex не видны) */
  tr.drop-before td,
  tr.drop-after td {
    border: none;
  }
  tr.drop-before {
    box-shadow: inset 0 2px 0 var(--accent);
  }
  tr.drop-after {
    box-shadow: inset 0 -2px 0 var(--accent);
  }
  /* «правил нет» - блоком */
  .rules tr td[colspan] {
    display: block;
  }
}
.cptext {
  width: 100%;
  box-sizing: border-box;
  font: 12px/1.5 ui-monospace, monospace;
  background: var(--bg-input, transparent);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 6px;
}
.duprow {
  margin-bottom: 10px;
}
.duppat {
  word-break: break-all;
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.dupgroups {
  font-size: 12px;
}
.muted {
  color: var(--dim);
}
</style>
