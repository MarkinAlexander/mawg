<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { UiButton, UiDialog } from '../components/ui'
import { api } from '../api/client'
import { useConfirm } from '../stores/confirm'
import { useSystem } from '../stores/system'
import { useToast } from '../stores/toast'
import { startUpdateWait } from './updateFlow'

// Бейдж «обновиться до X» в шапке (легаси #updBadge): проверка обновления
// один раз при старте (checkUpdates(false) - тихо), клик - подтверждение,
// установка и ожидание возврата панели с новой версией (updateFlow).
const { upd, checkUpdate } = useSystem();
const { confirm } = useConfirm();
const { push } = useToast();

const dlgOpen = ref(false);
const starting = ref(false);
const wait = ref<ReturnType<typeof startUpdateWait> | null>(null);

onMounted(() => {
  void checkUpdate().catch(() => {});
});
onBeforeUnmount(() => wait.value?.stop());

const waiting = () => wait.value?.state.phase === 'wait';

async function onClick() {
  const info = upd.info;
  if (!info?.update || starting.value || dlgOpen.value) return;
  const ok = await confirm({
    title: 'Обновление mawg',
    text: `Скачать и установить ${info.latest}? Файл проверяется на подлинность (sha256), служба перезапустится, панель на минуту уйдёт и откроется сама.`,
    okLabel: 'Обновить',
  });
  if (!ok) return;
  starting.value = true;
  try {
    await api('POST', '/system/update/run');
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
    starting.value = false;
    return;
  }
  // установка идёт в фоне после ответа: ждём возврата панели с новой
  // версией и перезагружаемся сами, иначе версия и бейдж отстают
  starting.value = false;
  wait.value = startUpdateWait(info.current, info.latest);
  dlgOpen.value = true;
}
</script>

<template>
  <button
    v-if="upd.info?.update"
    class="badge updbadge"
    :class="{ busy: starting }"
    :disabled="starting"
    :title="`текущая ${upd.info.current}; нажать - обновить (install.sh с проверкой sha256, сервис перезапустится)`"
    @click="onClick"
  >
    обновиться до {{ upd.info.latest }}
  </button>
  <UiDialog
    v-model:open="dlgOpen"
    title="Обновление mawg"
    :locked="!!wait && waiting()"
  >
    <div class="uwait">
      <span
        v-if="waiting()"
        class="uwait-spin"
      />
      <span class="uwait-text">{{ wait?.state.text }}</span>
      <span class="uwait-sec">{{ wait?.state.seconds }} с</span>
    </div>
    <template #footer>
      <UiButton
        v-if="wait && !waiting()"
        @click="dlgOpen = false"
      >
        Закрыть
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
/* легаси-стиль #updBadge: цвета --ok поверх глобального .badge;
   имя .updbadge не пересекается с легаси (GOTCHAS) */
.updbadge {
  cursor: pointer;
  border-color: var(--ok);
  color: var(--ok);
}
.updbadge:disabled {
  opacity: 0.55;
}
.uwait {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 12.5px;
}
.uwait-spin {
  flex: none;
  width: 14px;
  height: 14px;
  border: 2px solid var(--border);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: uwaitspin 0.8s linear infinite;
}
.uwait-sec {
  margin-left: auto;
  color: var(--dim);
  font-variant-numeric: tabular-nums;
  /* число и «с» неразрывно (слово владельца: в узкой карточке счётчик
     рвался вертикально «0 / с») и не сжимается флекс-соседями */
  white-space: nowrap;
  flex: none;
}
@keyframes uwaitspin {
  to {
    transform: rotate(360deg);
  }
}
</style>
