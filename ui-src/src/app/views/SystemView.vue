<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { UiBadge, UiBanner, UiButton, UiCard, UiDialog, UiHelp, UiHint, UiInput, UiSelect, type UiSelectOption, UiSwitch, UiTable, type UiTableColumn } from '../../components/ui'
import { useEvents } from '../../stores/events'
import { useStatus } from '../../stores/status'
import { useSystem } from '../../stores/system'
import { useConfirm } from '../../stores/confirm'
import { useToast } from '../../stores/toast'
import type { AppEvent, CoreInfo, SystemItem } from '../../api/types'
import { useSecrets } from '../secrets'
import LxInstallDialog from './LxInstallDialog.vue'

// Журнал событий (легаси refreshLog): хвост 80, НОВЫЕ СВЕРХУ, k-цвета
// пяти видов. Место - вкладка «Система» (44-дока §6). Опрос таб-локальный:
// start/stop на монтировании вьюхи, интервал 15с как легаси.
const { state, start, stop } = useEvents();
// приватный режим: строка журнала размыта, только если в ней адрес из конфигов
const secrets = useSecrets();

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

// Ф5 §7 «страница событий с фильтрами»: тип события (из данных, не
// захардкожен) + поиск по пулу/тексту. Фильтр применяется ко всему
// списку, хвост 80 - уже отфильтрованного.
const evKind = ref('');
const evQuery = ref('');
// человеческие лейблы типов событий (ключи kind - API, не трогаем)
const KIND_RU: Record<string, string> = {
  rotate: 'ротация', applied: 'применено', fallback: 'фоллбек',
  magitrickle: 'MagiTrickle', install: 'установка', policy: 'политика',
  sources: 'источник', probe: 'проверка', subupdate: 'подписка',
  restore: 'восстановление', 'smart-cascade': 'умный каскад',
  cascade: 'каскад', slots: 'слоты', warp: 'WARP',
};
const kindRu = (k: string) => KIND_RU[k] || k;
const evKindOptions = computed<UiSelectOption[]>(() => [
  { value: '', label: 'все события' },
  ...[...new Set(state.list.map((e) => e.kind))].sort().map((k) => ({ value: k, label: kindRu(k) })),
]);
const evFiltered = computed<AppEvent[]>(() => {
  const q = evQuery.value.trim().toLowerCase();
  return state.list
    .filter(
      (e) =>
        (!evKind.value || e.kind === evKind.value) &&
        (!q || e.message.toLowerCase().includes(q) || (e.pool || '').toLowerCase().includes(q)),
    )
    .slice(-80)
    .reverse();
});
const evFiltering = computed(() => !!evKind.value || !!evQuery.value.trim());

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

// --- установка компонента (легаси installComp 2890 + dlgLx 2928); диалог
// ядра sing-box-lx (профиль/бэкап) - общий компонент LxInstallDialog, он
// нужен и в потоке создания пула ---
const lxOpen = ref(false);

// установка идёт минуты (MagiTrickle качает релиз из GitHub) - молчаливое
// ожидание пугало: confirm закрывался, результат не приходил, кнопку можно
// было нажать второй раз (слово владельца 2026-10-18). Держим диалог хода
// со спиннером до результата, кнопки блокируем.
const busyId = ref('');
const busyText = computed(() =>
  busyId.value === 'magitrickle'
    ? 'Качаем релиз форка из GitHub и обновляем штатным менеджером пакетов; конфиг и группы сохраняются, служба MagiTrickle перезапустится. Это занимает до пары минут.'
    : 'Идёт установка. Это может занять до пары минут - не закрывайте панель.',
);

async function openInstall(item: SystemItem): Promise<void> {
  if (busyId.value) return;
  // легаси installComp перечитывает /system/check - предмет свежим
  await sys.refresh();
  const fresh = items.value.find((i) => i.id === item.id);
  if (!fresh) return;
  if (fresh.id === 'singbox-lx') {
    lxOpen.value = true;
    return;
  }
  if (!fresh.confirm) return;
  const okLabel = fresh.actionLabel || 'Установить';
  const ok = await confirm({ title: `${okLabel}: ${fresh.title}`, text: fresh.confirm, okLabel });
  if (!ok) return;
  await runInstall({ id: fresh.id });
}

async function runInstall(payload: { id: string; flavor?: string; backup?: boolean }): Promise<void> {
  busyId.value = payload.id;
  try {
    const r = await sys.install(payload);
    showResult(!!r.ok, r.output || '', r.ok ? '' : r.error || 'неизвестная ошибка');
  } catch (e) {
    showResult(false, '', e instanceof Error ? e.message : String(e));
  } finally {
    busyId.value = '';
  }
}

function showResult(ok: boolean, output: string, errText: string): void {
  // платформа и версия mawg - обязательно первой строкой (слово владельца:
  // пользователи присылают ошибки без указания, где они произошли)
  const meta = [status.data?.version, status.data?.backendName].filter(Boolean).join(' · ');
  result.value = { open: true, ok, out: (meta ? meta + '\n' : '') + (errText ? errText + '\n\n' : '') + output.trim() };
  void sys.refresh(); // легаси: после установки перечитываем системную проверку
}
const result = ref<{ open: boolean; ok: boolean; out: string } | null>(null);
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
  // честные состояния (слово владельца 2026-10-17): «не запущено» -
  // процесса нет; «запущено, но не отвечает» - API молчит
  const state = !eng.running
    ? 'ядро не запущено'
    : eng.api === false
      ? 'ядро запущено, но не отвечает'
      : 'ядро работает';
  const parts = [state];
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
// --- «Ядра VPN»: lifecycle ядра sing-box (start/stop/автозагрузка/удаление)
const coreBusy = ref('');
async function corePower(c: CoreInfo, action: 'start' | 'stop' | 'enable' | 'disable'): Promise<void> {
  if (action === 'stop') {
    const ok = await confirm({
      title: 'Остановить ' + c.title,
      text: 'Ядро выключается совсем: движковые пулы (tun) перестанут работать, пока вы не включите его обратно. Нативные пулы не затрагиваются.',
      okLabel: 'Остановить',
    });
    if (!ok) return;
  }
  coreBusy.value = action;
  try {
    await sys.corePower(c.id, action);
    push(action === 'start' ? 'Ядро включено, конфиг применён' : action === 'stop' ? 'Ядро остановлено' : 'Автозагрузка сервиса sing-box: ' + (action === 'enable' ? 'включена' : 'выключена'));
    void refreshStatus();
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  coreBusy.value = '';
}
async function coreUninstall(c: CoreInfo): Promise<void> {
  const ok = await confirm({
    title: 'Удалить ' + c.title,
    text: 'Ядро lx удаляется полностью (маркер mawg). Если при установке сохранялся ваш прежний sing-box - он вернётся на место. Движковые пулы перестанут работать, пока ядро не установить снова.',
    okLabel: 'Удалить',
  });
  if (!ok) return;
  coreBusy.value = 'uninstall';
  try {
    await sys.corePower(c.id, 'uninstall');
    push('Ядро удалено');
    void refreshStatus();
    void sys.refresh();
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  coreBusy.value = '';
}
const coreStateBadge = (c: CoreInfo): { text: string; variant: 'ok' | 'err' | 'warn' } => {
  if (c.disabled) return { text: 'выключен', variant: 'err' };
  return c.running ? { text: 'работает', variant: 'ok' } : { text: 'остановлен', variant: 'warn' };
};

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
          :busy="busyId === row.id"
          :disabled="!!busyId"
          @click="openInstall(row)"
        >
          {{ row.actionLabel || 'установить' }}
        </UiButton>
      </template>
    </UiTable>

    <div class="sect">
      Ядра VPN<UiHelp text="Жизненный цикл туннельных движков: включить/выключить совсем, автозагрузка сервиса, удаление. Сейчас - sing-box; список строится по данным роутера и готов к другим ядрам (mihomo и подобные). Остановка выключает движок целиком: движковые пулы перестают работать, нативные (WireGuard-слоты) не затрагиваются." />
    </div>
    <div
      v-for="c in sys.cores.list"
      :key="c.id"
      class="corerow"
    >
      <div class="coreline">
        <b>{{ c.title }}</b>
        <template v-if="c.installed">
          <UiBadge
            :text="c.version || '?'"
            variant="info"
          />
          <UiBadge
            v-if="c.lx"
            text="lx"
            variant="ok"
          />
          <UiBadge
            :text="c.mode || '?'"
            variant="muted"
          />
          <UiBadge
            :text="coreStateBadge(c).text"
            :variant="coreStateBadge(c).variant"
          />
          <UiBadge
            v-if="c.mode === 'shared'"
            :text="c.autostart ? 'автозагрузка вкл' : 'автозагрузка выкл'"
            :variant="c.autostart ? 'ok' : 'warn'"
            tooltip="автозапуск сервиса sing-box при загрузке роутера"
          />
          <span
            v-if="c.autostartNote"
            class="corenote"
          >{{ c.autostartNote }}</span>
        </template>
        <UiBadge
          v-else
          text="не установлен"
          variant="muted"
        />
      </div>
      <div
        v-if="c.installed"
        class="coreact"
      >
        <UiButton
          v-if="c.caps.start"
          variant="primary"
          :busy="coreBusy === 'start'"
          @click="corePower(c, 'start')"
        >
          Запустить
        </UiButton>
        <UiButton
          v-if="c.caps.stop"
          :busy="coreBusy === 'stop'"
          @click="corePower(c, 'stop')"
        >
          Остановить
        </UiButton>
        <UiSwitch
          v-if="c.mode === 'shared'"
          :model-value="c.autostart"
          :busy="coreBusy === 'enable' || coreBusy === 'disable'"
          label="автозагрузка"
          @update:model-value="(v: boolean) => corePower(c, v ? 'enable' : 'disable')"
        />
        <UiButton
          v-if="c.caps.uninstall"
          variant="danger"
          :busy="coreBusy === 'uninstall'"
          @click="coreUninstall(c)"
        >
          Удалить ядро
        </UiButton>
      </div>
      <UiHint
        v-if="c.note"
        :text="c.note"
      />
    </div>
    <UiHint text="«Свой экземпляр» - панель держит движок сама; «Общее ядро» - пулы живут внутри вашего sing-box, обновляются без разрыва соединений." />
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

  <LxInstallDialog v-model:open="lxOpen" />

  <UiDialog
    :open="!!busyId"
    title="Идёт установка"
    width="440px"
    :locked="true"
  >
    <div class="busy">
      <span class="busy-spin" />
      <span class="busy-text">{{ busyText }}</span>
    </div>
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
    <div class="evfilters">
      <UiSelect
        v-model="evKind"
        :options="evKindOptions"
        label="Тип события"
      />
      <UiInput
        v-model="evQuery"
        label="Поиск"
        placeholder="по пулу или тексту"
      />
    </div>
    <div
      v-if="evFiltering"
      class="evcount"
    >
      показано {{ evFiltered.length }} из {{ state.list.length }}
    </div>
    <div class="log">
      <!-- secret по содержимому: строки с адресами из конфигов; прочие
           события видны (слово владельца 2026-10-15) -->
      <div
        v-for="(ev, i) in evFiltered"
        :key="i"
        :class="{ secret: secrets.line(ev.message) }"
      >
        [{{ fmtTime(ev.time) }}] {{ ev.pool }}:
        <span
          :class="'k-' + ev.kind"
          :title="ev.kind"
        >{{ kindRu(ev.kind) }}</span>
        {{ ev.message }}
      </div>
    </div>
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
.evfilters {
  display: flex;
  gap: 10px;
  align-items: flex-end;
  margin-bottom: 8px;
}
.evfilters > :first-child {
  flex: 0 1 240px;
}
.evfilters > :last-child {
  flex: 1;
}
.evcount {
  color: var(--dim);
  font-size: 12px;
  margin-bottom: 6px;
}
.sect {
  font-weight: 600;
  font-size: 13px;
  margin: 16px 0 6px;
}
.corerow {
  padding: 8px 0 4px;
  border-bottom: 1px dashed var(--border);
}
.corerow:last-of-type {
  border-bottom: none;
}
.coreline {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}
.coreline > b {
  margin-right: 4px;
}
.corenote {
  color: var(--dim);
  font-size: 12px;
}
.coreact {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: center;
  margin-top: 6px;
}
.ver {
  color: var(--dim);
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
.busy {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 12.5px;
}
.busy-spin {
  flex: none;
  width: 14px;
  height: 14px;
  border: 2px solid var(--border);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: busyspin 0.8s linear infinite;
}
.busy-text {
  color: var(--dim);
}
@keyframes busyspin {
  to {
    transform: rotate(360deg);
  }
}
</style>
