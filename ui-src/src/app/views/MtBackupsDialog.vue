<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiBadge, UiButton, UiCheckbox, UiDialog, UiHint } from '../../components/ui'
import { useGroups } from '../../stores/groups'
import { useConfirm } from '../../stores/confirm'
import { useToast } from '../../stores/toast'
import type { BackupInfo, GroupView } from '../../api/types'
import {
  backupKind, dedupImportRules, exportStamp, importVerdict, parseImportFile,
  type ImportGroupSrc,
} from './groupsLogic'

// Единая секция резервных копий (легаси dlgMTBackups/dlgMTImport/dlgMTExport
// 603-655 + panel.js 1786-2010): список серверных бэкапов (скачать/
// восстановить), бэкап сейчас, импорт групп из файла (чекбоксы, режимы
// add/replace, дубли имён сняты в add) и выгрузка на ПК с выбором.

const open = defineModel<boolean>('open', { default: false });

const { state, loadBackups, createBackup, restoreBackup, importGroups } = useGroups();
const { push } = useToast();
const { confirm } = useConfirm();

const backupBusy = ref('');
const importOpen = ref(false);
const exportOpen = ref(false);

watch(open, (v) => {
  if (v) void loadBackups(); // свежий список при каждом открытии (легаси showMTBackupsDlg)
});

function kindLabel(b: BackupInfo): string {
  return backupKind(b.name).label;
}

async function download(b: BackupInfo): Promise<void> {
  window.location.href = '/api/v1/mt/backups/' + encodeURIComponent(b.name);
}

async function restore(b: BackupInfo): Promise<void> {
  const kind = backupKind(b.name).restoreAsk;
  const ok = await confirm({
    title: 'Восстановление из бэкапа',
    text: `Восстановить ${b.name}? Применится: ${kind}.`,
    okLabel: 'Восстановить',
  });
  if (!ok) return;
  backupBusy.value = b.name;
  try {
    await restoreBackup(b.name);
    push('Восстановлено из ' + b.name);
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  backupBusy.value = '';
}

async function backupNow(): Promise<void> {
  backupBusy.value = 'now';
  try {
    await createBackup();
    push('Бэкап MT-конфига и shadow сохранён в base/backups');
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  backupBusy.value = '';
}

// --- импорт из файла (легаси mtImportPick 1841) ---
const fileEl = ref<HTMLInputElement | null>(null);
const impParsed = ref<{ name: string; groups: ImportGroupSrc[]; bad: number } | null>(null);
const impMode = ref<'add' | 'replace'>('add');
const impChecked = ref<boolean[]>([]);
const impBusy = ref(false);

const impDup = computed(() => {
  if (!impParsed.value || impMode.value === 'replace') return new Set<number>();
  const names = new Set(state.groups.map((g) => g.name));
  return new Set(impParsed.value.groups.map((g, i) => (names.has(g.name) ? i : -1)).filter((i) => i >= 0));
});
const impInfo = computed(() => {
  if (!impParsed.value) return '';
  const rules = impParsed.value.groups.reduce((n, g) => n + g.rules.length, 0);
  return `Файл ${impParsed.value.name}: групп ${impParsed.value.groups.length}, правил ${rules}${impParsed.value.bad ? `, непригодных: ${impParsed.value.bad}` : ''}`;
});
const impCanGo = computed(() => impChecked.value.some(Boolean));

function pickFile(): void {
  fileEl.value?.click();
}

async function onFile(e: Event): Promise<void> {
  const el = e.target as HTMLInputElement;
  const f = el.files && el.files[0];
  el.value = ''; // повторный выбор того же файла тоже открывает модалку
  if (!f) return;
  const res = parseImportFile(f.name, await f.text());
  if (res.error) {
    push(res.error, true);
    return;
  }
  impParsed.value = { name: res.name!, groups: res.groups!, bad: res.bad || 0 };
  impMode.value = 'add'; // каждый импорт начинается с безопасного режима
  impChecked.value = res.groups!.map((g, i) => !impDup.value.has(i));
  importOpen.value = true;
}

function onImpMode(m: 'add' | 'replace'): void {
  impMode.value = m;
  if (impParsed.value) impChecked.value = impParsed.value.groups.map((_, i) => !impDup.value.has(i));
}

function impAll(v: boolean): void {
  impChecked.value = impChecked.value.map(() => v);
}

async function impGo(): Promise<void> {
  if (!impParsed.value || !impCanGo.value || impBusy.value) return;
  const chosen = impParsed.value.groups.filter((_, i) => impChecked.value[i]);
  const { groups, dedup } = dedupImportRules(chosen);
  impBusy.value = true;
  try {
    const r = await importGroups(impMode.value, groups);
    push(importVerdict(impMode.value, r, dedup));
    importOpen.value = false;
    open.value = false; // легаси закрывает и диалог бэкапов
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  impBusy.value = false;
}

// --- выгрузка на ПК (легаси 1968-2010) ---
const expChecked = ref<boolean[]>([]);
const expBusy = ref(false);

const expCanGo = computed(() => expChecked.value.some(Boolean));

function openExport(): void {
  if (!state.groups.length) {
    push('Групп нет - выгружать нечего', true);
    return;
  }
  expChecked.value = state.groups.map(() => true);
  exportOpen.value = true;
}

function groupLine(g: GroupView): string {
  return `${g.interface}${g.interfaceTitle ? ' - ' + g.interfaceTitle : ''} · правил: ${(g.rules || []).length} · ${g.enable ? 'вкл' : 'выкл'}`;
}

async function expGo(): Promise<void> {
  if (!expCanGo.value || expBusy.value) return;
  const chosen = state.groups.filter((_, i) => expChecked.value[i]);
  expBusy.value = true;
  try {
    // id оставляем - при импорте они всё равно выбрасываются (легаси 1966)
    const blob = new Blob([JSON.stringify({ groups: chosen }, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'mtrickle-groups-' + exportStamp(new Date()) + '.mtrickle';
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
    push('Выгружено групп: ' + chosen.length);
    exportOpen.value = false;
  } finally {
    expBusy.value = false;
  }
}
</script>

<template>
  <UiDialog
    v-model:open="open"
    title="Резервные копии правил"
    width="560px"
  >
    <UiHint text="Копии в base/backups: mt-shadow - правила в нашем формате (восстановление штатной записью с валидацией), mt-config - сырой config.yaml MagiTrickle (восстановление файла + рестарт демона). Перед каждой массовой записью копия делается автоматически, хранятся последние 10. Группы можно выгрузить файлом на ПК и импортировать обратно с выбором." />
    <div class="bkrow">
      <UiButton
        tooltip="скачать текущие группы файлом (с выбором) - вечная копия на ПК, серверные бэкапы ротируются"
        @click="openExport"
      >
        выгрузить группы в файл
      </UiButton>
      <UiButton
        tooltip="добавить выбранные группы из файла или заменить ими весь список: нативный config.mtrickle, наша тень, бэкап mt-shadow-*.conf"
        @click="pickFile"
      >
        импорт из файла
      </UiButton>
      <UiButton
        :busy="backupBusy === 'now'"
        tooltip="сохранить дисковый конфиг MagiTrickle и shadow-копию в base/backups"
        @click="backupNow"
      >
        создать бэкап сейчас
      </UiButton>
      <input
        ref="fileEl"
        type="file"
        accept=".mtrickle,.json,.conf"
        hidden
        @change="onFile"
      >
    </div>

    <UiHint
      v-if="state.backups.loaded && !state.backups.list.length"
      text="Бэкапов пока нет - сделайте первый кнопкой «создать бэкап сейчас»."
    />
    <div
      v-for="b in state.backups.list"
      :key="b.name"
      class="bkline"
    >
      <span class="bkname">
        <b>{{ kindLabel(b) }}</b>
        <span class="muted">{{ b.name }} · {{ b.size }} Б · {{ b.modified }}</span>
      </span>
      <span class="sp" />
      <UiButton @click="download(b)">
        скачать
      </UiButton>
      <UiButton
        :busy="backupBusy === b.name"
        @click="restore(b)"
      >
        восстановить
      </UiButton>
    </div>

    <template #footer>
      <UiButton
        variant="primary"
        @click="open = false"
      >
        Закрыть
      </UiButton>
    </template>
  </UiDialog>

  <!-- импорт групп из файла (легаси dlgMTImport) -->
  <UiDialog
    v-model:open="importOpen"
    title="Импорт групп из файла"
    :busy="impBusy"
    width="560px"
  >
    <UiHint :text="impInfo" />
    <div class="bkrow">
      <UiButton @click="impAll(true)">
        выбрать все
      </UiButton>
      <UiButton @click="impAll(false)">
        снять все
      </UiButton>
    </div>
    <div
      v-for="(g, i) in impParsed?.groups || []"
      :key="g.name"
      class="bkline"
    >
      <UiCheckbox
        :model-value="impChecked[i] === true"
        :tooltip="impDup.has(i) ? 'группа с таким именем уже есть - при добавлении будет пропущена' : ''"
        @update:model-value="(v: boolean) => { impChecked[i] = v }"
      />
      <span class="bkname">
        <b>{{ g.name }}</b>
        <span class="muted">{{ g.interface }} · правил: {{ g.rules.length }} · {{ g.enable ? 'вкл' : 'выкл' }}</span>
      </span>
      <span class="sp" />
      <UiBadge
        v-if="impDup.has(i)"
        text="дубль имени"
        variant="warn"
        tooltip="группа с таким именем уже есть - при добавлении будет пропущена"
      />
    </div>
    <div class="modes">
      <label class="mode">
        <input
          type="radio"
          value="add"
          :checked="impMode === 'add'"
          @change="onImpMode('add')"
        >
        добавить выбранные к существующим
      </label>
      <label class="mode">
        <input
          type="radio"
          value="replace"
          :checked="impMode === 'replace'"
          @change="onImpMode('replace')"
        >
        заменить ВСЕ группы на выбранные
      </label>
      <UiHint
        v-if="impMode === 'replace'"
        class="replwarn"
        text="Текущие группы будут удалены и заменены выбранными из файла. Каскады перестроятся."
      />
    </div>
    <template #footer>
      <UiButton
        :disabled="impBusy"
        @click="importOpen = false"
      >
        отмена
      </UiButton>
      <UiButton
        variant="primary"
        :busy="impBusy"
        :disabled="!impCanGo"
        @click="impGo"
      >
        импортировать
      </UiButton>
    </template>
  </UiDialog>

  <!-- выгрузка групп на ПК (легаси dlgMTExport) -->
  <UiDialog
    v-model:open="exportOpen"
    title="Выгрузка групп в файл"
    :busy="expBusy"
    width="560px"
  >
    <UiHint text="Формат тот же, что у нативного экспорта config.mtrickle и бэкапов shadow: файл на ПК - вечная копия (серверные бэкапы ротируются, последние 10), импортируется здесь же или в нативной панели." />
    <div class="bkrow">
      <UiButton @click="expChecked = expChecked.map(() => true)">
        выбрать все
      </UiButton>
      <UiButton @click="expChecked = expChecked.map(() => false)">
        снять все
      </UiButton>
    </div>
    <div
      v-for="(g, i) in state.groups"
      :key="g.id"
      class="bkline"
    >
      <UiCheckbox
        :model-value="expChecked[i] === true"
        @update:model-value="(v: boolean) => { expChecked[i] = v }"
      />
      <span class="bkname">
        <b>{{ g.name }}</b>
        <span class="muted">{{ groupLine(g) }}</span>
      </span>
      <span class="sp" />
    </div>
    <template #footer>
      <UiButton @click="exportOpen = false">
        отмена
      </UiButton>
      <UiButton
        variant="primary"
        :busy="expBusy"
        :disabled="!expCanGo"
        @click="expGo"
      >
        скачать файл
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.bkrow {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  justify-content: flex-end;
  margin: 6px 0;
}
.bkline {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 5px 0;
  border-bottom: 1px solid var(--border);
}
.bkline:last-of-type {
  border-bottom: 0;
}
.bkname {
  display: flex;
  gap: 6px;
  align-items: baseline;
  flex-wrap: wrap;
  word-break: break-all;
}
.sp {
  flex: 1;
}
.modes {
  display: grid;
  gap: 5px;
  margin: 8px 0 2px;
}
.mode {
  display: flex;
  gap: 8px;
  align-items: center;
  font-size: 13px;
}
.replwarn {
  color: var(--err);
}
.muted {
  color: var(--dim);
  font-size: 12px;
}
</style>
