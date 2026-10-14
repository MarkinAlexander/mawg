<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { UiBadge, UiButton, UiCheckbox, UiCard, UiHint, UiSelect, type UiSelectOption, UiSwitch, UiTextarea } from '../../components/ui'
import { useGroups } from '../../stores/groups'
import { useConfirm } from '../../stores/confirm'
import { useToast } from '../../stores/toast'
import type { BackupInfo, GroupView } from '../../api/types'
import {
  backupKind, dedupImportRules, exportStamp, importVerdict, parseImportFile, parseSources,
  SRC_MODE_OPTIONS,
  type ImportGroupSrc,
} from './groupsLogic'

// Страница «Резервные копии» (44-дока §6, слово владельца: вынести из
// диалога в разделе групп раскрытым видом). Серверные копии - полный
// архив панели (mawg-full: настройки, пулы, каскады, shadow, конфиг MT)
// и авто-верёвки MT (shadow/mt-config) перед массовыми записями. Ниже -
// перенос групп файлом: выгрузка на ПК и импорт с выбором, без модалок.

const { state, loadBackups, createBackup, restoreBackup, importGroups, setPolicy, start, stop } = useGroups();
const { push } = useToast();
const { confirm } = useConfirm();

onMounted(() => {
  start(); // группы для выгрузки/импорта-дублей
  void loadBackups(); // свежий список при входе на вкладку
});
onBeforeUnmount(stop);

const backupBusy = ref('');

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
    push('Полный бэкап сохранён: настройки панели, правила и конфиг MT');
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  backupBusy.value = '';
}

// --- выгрузка на ПК (легаси 1968-2010), всегда развёрнута ---
const expChecked = ref<boolean[]>([]);
const expBusy = ref(false);

const expCanGo = computed(() => expChecked.value.some(Boolean));

// группы приходят поллингом - чекбоксы держим синхронно по числу
watch(
  () => state.groups.length,
  (n) => {
    if (expChecked.value.length !== n) expChecked.value = Array(n).fill(true);
  },
  { immediate: true },
);

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
  } finally {
    expBusy.value = false;
  }
}

// --- импорт из файла (легаси mtImportPick 1841), инлайн без модалки ---
const fileEl = ref<HTMLInputElement | null>(null);
const impParsed = ref<{ name: string; groups: ImportGroupSrc[]; bad: number } | null>(null);
const impMode = ref<'add' | 'replace'>('add');
const impChecked = ref<boolean[]>([]);
const impBusy = ref(false);

// Ф5 §7 «редактор sources у импорта»: режим задаётся ВСЕМ импортированным
// группам (policy PUT после записи; в файле .mtrickle источников нет).
// '' = не задавать; старый MT без sources - селектор скрыт (sourcesCapable).
const impSources = ref<'' | 'all' | 'all+router' | 'list'>('');
const impSrcList = ref('');
const impSrcRouter = ref(false);
const impSrcOptions: UiSelectOption[] = [
  { value: '', label: 'не задавать - как в файле' },
  ...SRC_MODE_OPTIONS,
];

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

async function onFile(e: Event): Promise<void> {
  const el = e.target as HTMLInputElement;
  const f = el.files && el.files[0];
  el.value = ''; // повторный выбор того же файла тоже читается
  if (!f) return;
  const res = parseImportFile(f.name, await f.text());
  if (res.error) {
    push(res.error, true);
    return;
  }
  impParsed.value = { name: res.name!, groups: res.groups!, bad: res.bad || 0 };
  impMode.value = 'add'; // каждый импорт начинается с безопасного режима
  impChecked.value = res.groups!.map((g, i) => !impDup.value.has(i));
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
  const srcMode = state.sourcesCapable ? impSources.value : '';
  const srcList = srcMode === 'list' ? parseSources(impSrcList.value) : [];
  // паритет GroupDialog: пустой список без роутера - не «вся локалка»
  const srcRouter = srcMode === 'list' && impSrcRouter.value;
  if (srcMode === 'list' && !srcList.length && !srcRouter) {
    push('Пустой список источников: добавьте IP или включите роутер', true);
    return;
  }
  const chosen = impParsed.value.groups.filter((_, i) => impChecked.value[i]);
  const { groups, dedup } = dedupImportRules(chosen);
  impBusy.value = true;
  try {
    // имена ДО импорта: при add новые - только те, чего не было (дубли
    // имён бэкенд пропускает); при replace новыми будут все выбранные
    const preNames = new Set(state.groups.map((g) => g.name));
    const r = await importGroups(impMode.value, groups);
    let srcNote = '';
    if (srcMode) {
      const skipped = new Set(r.skipped || []);
      const chosenNames = new Set(groups.map((g) => g.name));
      const targets = state.groups.filter(
        (g) =>
          chosenNames.has(g.name) &&
          !skipped.has(g.name) &&
          (impMode.value === 'replace' || !preNames.has(g.name)),
      );
      const failed: string[] = [];
      let okN = 0;
      for (const g of targets) {
        try {
          // mtSetPolicy заменяет политику ЦЕЛИКОМ (GOTCHAS) - шлются все
          // пять полей; у свежей группы onDead/iface пустые
          await setPolicy(g.id, { onDead: '', iface: '', sourcesMode: srcMode, sources: srcList, sourcesRouter: srcRouter });
          okN++;
        } catch (e) {
          failed.push(`${g.name} (${e instanceof Error ? e.message : String(e)})`);
        }
      }
      if (okN) srcNote += `; источники заданы: ${okN}`;
      if (failed.length) srcNote += `; НЕ заданы: ${failed.join(', ')}`;
    }
    push(importVerdict(impMode.value, r, dedup) + srcNote);
    impParsed.value = null; // секция импорта сворачивается до выбора файла
    impSources.value = '';
    impSrcList.value = '';
    impSrcRouter.value = false;
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  impBusy.value = false;
}
</script>

<template>
  <UiCard
    id="card-backups"
    title="Резервные копии"
    subtitle="хранятся на роутере в base/backups, по 10 последних каждого вида"
  >
    <UiHint text="«Полный» - вся панель одним архивом: настройки (порт, доступ, RCI), пулы, каскады, наборы, правила, конфиг MagiTrickle. shadow - правила (восстановление штатной записью), MT-конфиг - сырой config.yaml MagiTrickle (файл + рестарт демона). Перед каждой массовой записью MT-копии делаются автоматически." />
    <div class="bkrow">
      <UiButton
        :busy="backupBusy === 'now'"
        tooltip="полный архив панели (mawg-full.tar.gz) плюс копии конфига MT и shadow"
        @click="backupNow"
      >
        создать бэкап сейчас
      </UiButton>
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
  </UiCard>

  <UiCard
    id="card-transfer"
    title="Импорт и выгрузка групп"
    subtitle="группы правил отдельным файлом - вечная копия на ПК и перенос между панелями"
  >
    <div class="sect">
      Выгрузка на ПК
    </div>
    <UiHint text="Формат тот же, что у нативного config.mtrickle и бэкапов shadow: серверные копии ротируются, файл на ПК - нет. Импортируется здесь же или в нативной панели." />
    <div class="bkrow">
      <UiButton @click="expChecked = expChecked.map(() => true)">
        выбрать все
      </UiButton>
      <UiButton @click="expChecked = expChecked.map(() => false)">
        снять все
      </UiButton>
      <UiButton
        variant="primary"
        :busy="expBusy"
        :disabled="!expCanGo"
        tooltip="скачать выбранные группы файлом .mtrickle"
        @click="expGo"
      >
        скачать файл ({{ expChecked.filter(Boolean).length }})
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
    <UiHint
      v-if="state.loaded && !state.groups.length"
      text="Групп нет - выгружать нечего."
    />

    <div class="sect">
      Импорт из файла
    </div>
    <UiHint text="Нативный config.mtrickle, наша тень или бэкап mt-shadow-*.conf. После выбора файла - список с галочками: дубли имён снимаются заранее, добавление идёт к существующим или заменяет весь список." />
    <div class="bkrow">
      <UiButton
        tooltip="выбрать файл с группами"
        @click="fileEl?.click()"
      >
        {{ impParsed ? 'выбрать другой файл...' : 'выбрать файл...' }}
      </UiButton>
      <input
        ref="fileEl"
        type="file"
        accept=".mtrickle,.json,.conf"
        hidden
        @change="onFile"
      >
    </div>
    <template v-if="impParsed">
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
        v-for="(g, i) in impParsed.groups"
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
      <!-- Ф5 §7: режим «кто ходит в группу» для всех импортированных -
           у старого MT селектора нет (sourcesCapable) -->
      <template v-if="state.sourcesCapable">
        <UiSelect
          v-model="impSources"
          :options="impSrcOptions"
          label="Источники импортированных групп"
        />
        <template v-if="impSources === 'list'">
          <UiTextarea
            v-model="impSrcList"
            label="IP или CIDR через запятую"
            :rows="3"
            mono
          />
          <UiSwitch
            v-model="impSrcRouter"
            label="Включая роутер"
            hint="его адреса добавятся в источники сами"
          />
        </template>
      </template>
      <div class="bkrow">
        <UiButton
          variant="primary"
          :busy="impBusy"
          :disabled="!impCanGo"
          @click="impGo"
        >
          импортировать
        </UiButton>
      </div>
    </template>
  </UiCard>
</template>

<style scoped>
.sect {
  color: var(--dim);
  font-size: 12px;
  text-transform: uppercase;
  letter-spacing: 0.4px;
  margin-top: 6px;
}
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
