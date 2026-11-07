<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { UiBadge, UiButton, UiCard, UiDialog, UiHint, UiIcon, UiTable } from '../../components/ui'
import { useStatus } from '../../stores/status'
import { useSystem } from '../../stores/system'
import { useConfirm } from '../../stores/confirm'
import { useToast } from '../../stores/toast'
import type { SystemItem } from '../../api/types'
import LxInstallDialog from './LxInstallDialog.vue'
import SystemCores from './SystemCores.vue'
import SystemDiag from './SystemDiag.vue'
import SystemJournal from './SystemJournal.vue'
import { SYSTEM_COLUMNS, busyTextOf, sourceLabel } from './systemLogic'

// Вкладка «Система»: таблица зависимостей (проверка/установка) и рестарт
// MT - здесь; ядра VPN с режимом движка - SystemCores, диагностика
// движков - SystemDiag, журнал событий - SystemJournal, прямые ссылки на
// бинари - SystemDownloads, чистые функции - systemLogic.ts.
const sys = useSystem();

// системная проверка - на этой же вкладке раз в 60с, форс - по кнопке
// (легаси refreshSystem)
onMounted(() => {
  void sys.start(60000);
});
onBeforeUnmount(() => {
  sys.stop();
});

const { state: status } = useStatus();
const { confirm } = useConfirm();
const { push } = useToast();

const items = computed<SystemItem[]>(() => sys.state.data?.items ?? []);

const refreshing = ref(false);
async function refreshSystem(): Promise<void> {
  refreshing.value = true;
  try {
    await sys.refresh();
  } finally {
    refreshing.value = false;
  }
}

// рестарт активного MagiTrickle (слово владельца 03.11): геоблок/правила
// удобно проверять перезапуском MT прямо из панели; хуки пересобираются,
// маршрутизация мигнёт на секунды - пулы mawg не трогаются
const mtBusy = ref(false);
async function mtRestart(): Promise<void> {
  if (mtBusy.value) return;
  const ok = await confirm({
    title: 'Перезапустить MagiTrickle?',
    text: 'Хуки групп пересоберутся: маршрутизация по правилам мигнёт на пару секунд (сессии телеграма/геоблока могут оборваться). Пулы mawg и их туннели не трогаются.',
    okLabel: 'Перезапустить',
  });
  if (!ok) return;
  mtBusy.value = true;
  try {
    await sys.mtRestart();
    push('MagiTrickle перезапущен', false);
    void sys.refresh();
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  } finally {
    mtBusy.value = false;
  }
}

// установка компонента (легаси installComp 2890 + dlgLx 2928); диалог ядра
// sing-box-lx - общий компонент LxInstallDialog, нужен и в создании пула
const lxOpen = ref(false);

// установка идёт минуты (MagiTrickle качает релиз из GitHub) - молчаливое
// ожидание пугало: confirm закрывался, результат не приходил, кнопку можно
// было нажать второй раз (слово владельца 2026-10-18). Держим диалог хода
// со спиннером до результата, кнопки блокируем.
const busyId = ref('');
const busyText = computed(() => busyTextOf(busyId.value));

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
  // mihomo в таблице - согласие уже дано confirm'ом: replace=true, чтобы
  // панель не отказывалась менять чужой бинарь без второго подтверждения
  await runInstall(fresh.id === 'mihomo' ? { id: fresh.id, replace: true } : { id: fresh.id });
}

async function runInstall(payload: { id: string; flavor?: string; source?: string; backup?: boolean; replace?: boolean }): Promise<void> {
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
  void sys.refresh();
  void sys.refreshCores(); // строка ядра могла ожить/обновиться
}
const result = ref<{ open: boolean; ok: boolean; out: string } | null>(null);
const resultOpen = computed({
  get: () => result.value?.open ?? false,
  set: (v: boolean) => {
    if (result.value) result.value.open = v;
  },
});
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
        :busy="mtBusy"
        tooltip="рестарт активного MagiTrickle: хуки групп пересобираются, маршрутизация мигнёт на секунды"
        @click="mtRestart"
      >
        перезапустить MT
      </UiButton>
      <!-- круглая стрелка вместо слова «обновить» (слово владельца 04.11:
           читалось как «обновление всего», а это лишь перечитывание
           состояний и статусов); крупная и заметная - слово владельца:
           «если бы не знал, что она есть - не нашёл бы» -->
      <UiButton
        variant="icon"
        class="refresh-btn"
        :busy="refreshing"
        tooltip="перечитать состояния и статусы (это не обновление программ)"
        @click="refreshSystem"
      >
        <UiIcon name="refresh-bold" />
      </UiButton>
    </template>
    <UiTable
      :columns="SYSTEM_COLUMNS"
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
        <UiBadge
          v-if="row.source"
          :text="sourceLabel(row.source)"
          variant="info"
        />
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

    <SystemCores
      :busy-id="busyId"
      :run-install="runInstall"
    />
    <SystemDiag />
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

  <SystemJournal />
</template>

<style scoped>
/* кнопка-стрелка «перечитать состояния»: иконка сама 18×18
   (refresh-bold), паддинг как у текстовых - высота совпадает с
   «перезапустить MT» (слово владельца 04.11: видно, но не выше соседей) */
.refresh-btn {
  padding: 5px;
}
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
