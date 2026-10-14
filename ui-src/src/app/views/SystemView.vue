<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { UiBadge, UiBanner, UiButton, UiCard, UiDialog, UiHint, UiSelect, type UiSelectOption, UiSwitch, UiTable, type UiTableColumn } from '../../components/ui'
import { useEvents } from '../../stores/events'
import { useStatus } from '../../stores/status'
import { useSystem } from '../../stores/system'
import { useConfirm } from '../../stores/confirm'
import { useToast } from '../../stores/toast'
import type { AppEvent, SystemItem } from '../../api/types'

// Журнал событий (легаси refreshLog): хвост 80, НОВЫЕ СВЕРХУ, k-цвета
// пяти видов. Место - вкладка «Система» (44-дока §6). Опрос таб-локальный:
// start/stop на монтировании вьюхи, интервал 15с как легаси.
const { state, start, stop } = useEvents();

// системная проверка опрашивается на этой же вкладке раз в 60с (легаси
// startPanel: setInterval(refreshSystem, 60000)), форс - по кнопке
onMounted(() => {
  start();
  void sys.start(60000);
});
onBeforeUnmount(() => {
  stop();
  sys.stop();
});

// UiCard - контролируемый: без v-model свёртывание не работает
const collapsed = ref(false);

const tail = computed<AppEvent[]>(() => state.list.slice(-80).reverse());

// легаси fmtTime: toLocaleTimeString('ru-RU')
function fmtTime(iso: string): string {
  return iso ? new Date(iso).toLocaleTimeString('ru-RU') : '';
}

// --- Системная проверка (легаси refreshSystem/systable 2385-2405) ---
const sys = useSystem();
const { state: status, refresh: refreshStatus } = useStatus();
const { confirm } = useConfirm();
const { push } = useToast();

const items = computed<SystemItem[]>(() => sys.state.data?.items ?? []);
const columns: UiTableColumn[] = [
  { key: 'title', label: 'компонент' },
  { key: 'version', label: 'версия' },
  { key: 'state', label: 'состояние' },
  { key: 'action', label: '' },
];
const refreshing = ref(false);
async function refreshSystem(): Promise<void> {
  refreshing.value = true;
  try {
    await sys.refresh();
  } finally {
    refreshing.value = false;
  }
}

// --- установка компонента (легаси installComp 2890 + dlgLx 2928) ---
const dlg = ref<{
  open: boolean
  item: SystemItem | null
  phase: 'form' | 'progress' | 'result'
  flavor: string
  backup: boolean
  ok: boolean
  out: string
} | null>(null);

const flavorOptions: UiSelectOption[] = [
  { value: 'plain', label: 'plain - обычный бинарь: быстрый старт, много места (на mips ~90 МБ)' },
  { value: 'upx', label: 'upx - сжатый: в ~4 раза меньше на диске, старт дольше и RAM больше (opt-in)' },
];
const mb = (b: number) => (b / 1048576).toFixed(0) + ' МБ';
const isLxReplace = computed(() => dlg.value?.item?.action === 'singbox-lx-replace');
const backupFits = computed(() => {
  const it = dlg.value?.item;
  return !!it && (it.freeBytes || 0) > (it.backupBytes || 0) + 64 * 1048576;
});
const backupText = computed(() => {
  const it = dlg.value?.item;
  if (!it) return '';
  return backupFits.value
    ? `сохранить старое ядро рядом (sing-box.pre-lx, ${mb(it.backupBytes || 0)}, свободно ${mb(it.freeBytes || 0)})`
    : `для копии старого ядра (${mb(it.backupBytes || 0)}) места не хватит (свободно ${mb(it.freeBytes || 0)}) - будет удалено без сохранения`;
});
const dlgTitle = computed(() =>
  dlg.value?.item ? `${dlg.value.item.actionLabel || 'Установить'} - ${dlg.value.item.id === 'singbox-lx' ? 'ядро sing-box-lx' : dlg.value.item.title}` : '',
);

async function openInstall(item: SystemItem): Promise<void> {
  // легаси installComp перечитывает /system/check - предмет свежим
  await sys.refresh();
  const fresh = items.value.find((i) => i.id === item.id);
  if (!fresh) return;
  if (fresh.id === 'singbox-lx') {
    dlg.value = {
      open: true, item: fresh, phase: 'form',
      flavor: 'plain',
      backup: fresh.action === 'singbox-lx-replace' && (fresh.backupBytes || 0) > 0 && (fresh.freeBytes || 0) > (fresh.backupBytes || 0) + 64 * 1048576,
      ok: false, out: '',
    };
    return;
  }
  if (!fresh.confirm) return;
  const okLabel = fresh.actionLabel || 'Установить';
  const ok = await confirm({ title: `${okLabel}: ${fresh.title}`, text: fresh.confirm, okLabel });
  if (!ok) return;
  await runInstall({ id: fresh.id });
}

async function runInstall(payload: { id: string; flavor?: string; backup?: boolean }): Promise<void> {
  if (dlg.value) dlg.value.phase = 'progress';
  try {
    const r = await sys.install(payload);
    showResult(!!r.ok, r.output || '', r.ok ? '' : r.error || 'неизвестная ошибка');
  } catch (e) {
    showResult(false, '', e instanceof Error ? e.message : String(e));
  }
}
function installForm(): void {
  const it = dlg.value?.item;
  if (!it) return;
  void runInstall({ id: it.id, flavor: dlg.value?.flavor || 'plain', backup: dlg.value?.backup || false });
}

function showResult(ok: boolean, output: string, errText: string): void {
  const outText = (errText ? errText + '\n\n' : '') + output.trim();
  if (dlg.value) {
    dlg.value.ok = ok;
    dlg.value.out = outText;
    dlg.value.phase = 'result';
  } else {
    result.value = { open: true, ok, out: outText };
  }
  void sys.refresh(); // легаси: после установки перечитываем системную проверку
}
const result = ref<{ open: boolean; ok: boolean; out: string } | null>(null);
const dlgOpen = computed({
  get: () => dlg.value?.open ?? false,
  set: (v: boolean) => {
    if (dlg.value) dlg.value.open = v;
  },
});
const resultOpen = computed({
  get: () => result.value?.open ?? false,
  set: (v: boolean) => {
    if (result.value) result.value.open = v;
  },
});

// --- режим движка sing-box (легаси updateSbMode/setSbMode/cacheAct) ---
const sbMode = ref('own');
watch(
  () => status.data?.engine?.mode,
  (m) => {
    if (m) sbMode.value = m;
  },
  { immediate: true },
);
const engine = computed(() => status.data?.engine);
const engineInfo = computed(() => {
  const eng = engine.value;
  if (!eng || !eng.available) return 'движок не найден';
  const parts = [eng.running ? 'ядро работает' : 'ядро не отвечает'];
  if (eng.mode === 'shared' && eng.fragment) parts.push(eng.fragment);
  return parts.join(' | ');
});
const sbApplying = ref(false);
const sbModeOptions: UiSelectOption[] = [
  { value: 'own', label: 'свой экземпляр' },
  { value: 'shared', label: 'общее ядро (-C merge)' },
];
async function applySbMode(): Promise<void> {
  const mode = sbMode.value;
  const ok = await confirm({
    title: 'Сменить режим движка',
    text: mode === 'shared'
      ? 'Пулы mawg переедут в общий конфиг sing-box (mawg-pools.json в каталоге основного ядра). Ваш sing-box будет перезапущен - соединения коротко прервутся.'
      : 'Вернуть отдельный экземпляр движка mawg. Ваш sing-box будет перезапущен без фрагмента mawg.',
    okLabel: 'Сменить',
  });
  if (!ok) {
    sbMode.value = engine.value?.mode || 'own';
    return;
  }
  sbApplying.value = true;
  try {
    const r = await sys.setSingboxMode(mode);
    push(r.warning ? 'Режим сменён, но: ' + r.warning : 'Режим движка: ' + (r.mode || mode), !!r.warning);
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  sbApplying.value = false;
  setTimeout(() => void refreshStatus(), 500); // легаси setTimeout(refresh, 500)
}
const sbCacheWarn = computed(() => {
  const eng = engine.value;
  return !!eng && eng.mode === 'shared' && !!eng.cacheFile && !eng.cacheFile.startsWith('/tmp');
});
const SB_CACHE_TEXT = 'Кэш ядра пишется на флешку - постоянный износ ~170МБ/сутки. Выключить совсем: ничего не пишется, выборы сбрасываются при рестарте ядра. В /tmp: не пишется на флешку, выборы живут до ребута роутера.';
async function cacheAct(mode: 'off' | 'tmp'): Promise<void> {
  const texts = {
    off: ['Выключить кэш ядра', 'Кэш будет выключен (experimental.cache_file.enabled = false): запись на флешку прекратится совсем. Ваш sing-box будет перезапущен - соединения коротко прервутся. Запомненные выборы селекторов сбросятся - urltest-пулы восстановятся сами.', 'Выключить'],
    tmp: ['Перенести кэш ядра в /tmp', 'cache_file переедет в /tmp/sing-box/cache.db: на флешку писаться перестанет, выборы живут до ребута роутера. Ваш sing-box будет перезапущен - соединения коротко прервутся.', 'Перенести'],
  }[mode];
  const ok = await confirm({ title: texts[0], text: texts[1], okLabel: texts[2] });
  if (!ok) return;
  try {
    await sys.setSingboxCache(mode);
    push(mode === 'off' ? 'Кэш ядра выключен - запись на флешку прекращена' : 'Кэш перенесён в /tmp');
    void refreshStatus();
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
}
</script>

<template>
  <!-- id - якоря для поиска по настройкам (SearchDialog, search.ts) -->
  <UiCard
    id="card-system"
    title="Система"
    subtitle="Проверка зависимостей: пакеты WireGuard/AmneziaWG, MagiTrickle, ядро sing-box-lx, компоненты прошивки. Установка только по вашему подтверждению."
  >
    <template #actions>
      <UiButton
        :busy="refreshing"
        tooltip="перечитать системную проверку"
        @click="refreshSystem"
      >
        обновить
      </UiButton>
    </template>
    <UiTable
      :columns="columns"
      :rows="items"
      :row-key="(r: SystemItem) => r.id"
    >
      <template #cell-title="{ row }">
        {{ row.title }}
        <UiHint
          v-if="row.note"
          :text="row.note"
        />
      </template>
      <template #cell-version="{ row }">
        <span class="ver">{{ row.version || '' }}</span>
      </template>
      <template #cell-state="{ row }">
        <UiBadge
          v-if="row.statusText"
          :text="row.statusText"
          :variant="row.installed ? 'ok' : 'err'"
        />
        <UiBadge
          v-else
          :text="row.installed ? 'ок' : 'нет'"
          :variant="row.installed ? 'ok' : 'err'"
        />
      </template>
      <template #cell-action="{ row }">
        <UiButton
          v-if="row.action"
          variant="primary"
          @click="openInstall(row)"
        >
          {{ row.actionLabel || 'установить' }}
        </UiButton>
      </template>
    </UiTable>

    <div class="sect">
      Режим движка sing-box
    </div>
    <UiHint text="«Свой экземпляр» - mawg держит отдельный процесс sing-box (tun1+, mixed 2282+, clash 2291). «Общее ядро» - пулы mawg живут фрагментом mawg-pools.json рядом с config.json основного sing-box (сам config.json не меняется), применение - горячее: lx-ядро перечитывает конфиг по SIGHUP, туннели и соединения не рвутся (на upstream-ядре - рестарт сервиса). Пробы идут через clash_api основного ядра." />
    <UiSelect
      v-model="sbMode"
      :options="sbModeOptions"
      label="режим движка"
    >
      <template #action>
        <UiButton
          variant="primary"
          :busy="sbApplying"
          @click="applySbMode"
        >
          применить
        </UiButton>
      </template>
    </UiSelect>
    <UiHint :text="engineInfo" />
    <UiBanner
      v-if="sbCacheWarn"
      kind="warn"
      :text="SB_CACHE_TEXT"
    >
      <UiButton
        variant="primary"
        @click="cacheAct('off')"
      >
        выключить кэш
      </UiButton>
      <UiButton @click="cacheAct('tmp')">
        в /tmp
      </UiButton>
    </UiBanner>
  </UiCard>

  <UiDialog
    v-model:open="dlgOpen"
    :title="dlgTitle"
    :busy="dlg?.phase === 'progress'"
    :locked="dlg?.phase === 'progress'"
    width="480px"
  >
    <template v-if="dlg">
      <template v-if="dlg.phase === 'form'">
        <div class="lxtext">
          {{ dlg.item?.confirm }}
        </div>
        <UiSelect
          v-model="dlg.flavor"
          :options="flavorOptions"
          label="Профиль сборки"
        />
        <UiSwitch
          v-if="isLxReplace && (dlg.item?.backupBytes || 0) > 0"
          :model-value="dlg.backup"
          :label="backupText"
          tight
          :disabled="!backupFits"
          tooltip="сохранить старое ядро рядом перед заменой"
          @update:model-value="(v: boolean) => dlg && (dlg.backup = v)"
        />
        <UiHint
          v-if="dlg.item?.note"
          :text="dlg.item.note"
        />
      </template>
      <div
        v-else-if="dlg.phase === 'progress'"
        class="progress"
      >
        Идёт установка: качается релиз (~30-90 МБ), проверяется контрольная сумма, прогоняется тест-запуск. Это занимает до пары минут - не закрывайте панель.
      </div>
      <template v-else>
        <div
          class="rstitle"
          :class="dlg.ok ? 'ok' : 'err'"
        >
          {{ dlg.ok ? 'Установлено' : 'Ошибка установки' }}
        </div>
        <div class="rsout">
          {{ dlg.out }}
        </div>
      </template>
    </template>
    <template #footer>
      <template v-if="dlg?.phase === 'form'">
        <UiButton @click="dlgOpen = false">
          Отмена
        </UiButton>
        <UiButton
          variant="primary"
          @click="installForm"
        >
          {{ dlg?.item?.actionLabel || 'Установить' }}
        </UiButton>
      </template>
      <UiButton
        v-else-if="dlg?.phase === 'result'"
        variant="primary"
        @click="dlgOpen = false"
      >
        Закрыть
      </UiButton>
    </template>
  </UiDialog>

  <UiDialog
    v-model:open="resultOpen"
    :title="result?.ok ? 'Установлено' : 'Ошибка установки'"
    width="480px"
  >
    <div
      class="rstitle"
      :class="result?.ok ? 'ok' : 'err'"
    >
      {{ result?.ok ? 'Установлено' : 'Ошибка установки' }}
    </div>
    <div class="rsout">
      {{ result?.out }}
    </div>
    <template #footer>
      <UiButton
        variant="primary"
        @click="resultOpen = false"
      >
        Закрыть
      </UiButton>
    </template>
  </UiDialog>

  <UiCard
    id="card-events"
    v-model:collapsed="collapsed"
    title="Журнал событий"
    subtitle="последние 80 записей, новые сверху"
    collapsible
  >
    <div class="log">
      <div
        v-for="(ev, i) in tail"
        :key="i"
      >
        [{{ fmtTime(ev.time) }}] {{ ev.pool }}:
        <span :class="'k-' + ev.kind">{{ ev.kind }}</span>
        {{ ev.message }}
      </div>
    </div>
  </UiCard>
</template>

<style scoped>
.sect {
  font-weight: 600;
  font-size: 13px;
  margin: 16px 0 6px;
}
.ver {
  color: var(--dim);
}
.lxtext {
  font-size: 13px;
  white-space: pre-line;
  line-height: 1.5;
}
.progress {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13px;
  line-height: 1.5;
}
.rstitle {
  font-weight: 600;
}
.rstitle.ok {
  color: var(--ok);
}
.rstitle.err {
  color: var(--err);
}
.rsout {
  font: 11.5px/1.5 ui-monospace, monospace;
  color: var(--dim);
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 8px;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 40vh;
  overflow: auto;
}
</style>

<style scoped>
/* легаси #log; классы k-* scoped - с легаси-глобальными не пересекаются */
.log {
  font: 11.5px/1.5 ui-monospace, monospace;
  color: var(--dim);
  max-height: 220px;
  overflow: auto;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 8px;
}
.k-rotate,
.k-applied {
  color: var(--accent);
}
.k-fallback {
  color: var(--err);
}
.k-magitrickle,
.k-install {
  color: var(--warn);
}
</style>
