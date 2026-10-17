<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiButton, UiDialog, UiHelp, UiHint, UiSelect, UiSwitch, type UiSelectOption } from '../../components/ui'
import { useSystem } from '../../stores/system'
import type { SystemItem } from '../../api/types'
import { G } from '../glossary'

// Диалог установки ядра sing-box-lx: профиль сборки (plain/upx) и бэкап
// чужого ядра при замене. Общий для «Система -> Зависимости» и потока
// создания пула (слово владельца 2026-10-17: если движка нет - предлагать
// установку прямо при создании пула, а не отсылать в другой раздел).
const open = defineModel<boolean>('open', { default: false });
const emit = defineEmits<{ installed: [] }>();

const sys = useSystem();

const dlg = ref<{
  item: SystemItem | null
  phase: 'form' | 'progress' | 'result'
  flavor: string
  backup: boolean
  ok: boolean
  out: string
} | null>(null);

const flavorOptions: UiSelectOption[] = [
  { value: 'plain', label: 'обычный: быстрый старт, занимает больше места' },
  { value: 'upx', label: 'сжатый (upx): в ~4 раза меньше на диске, старт дольше' },
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
const dlgTitle = computed(() => (dlg.value?.item ? `${dlg.value.item.actionLabel || 'Установить'} - ядро sing-box-lx` : ''));

watch(open, async (v) => {
  if (!v) return;
  // предмет установки должен быть свежим (легаси installComp перечитывал
  // /system/check перед диалогом)
  await sys.refresh();
  const fresh = (sys.state.data?.items || []).find((i) => i.id === 'singbox-lx');
  if (!fresh) {
    open.value = false;
    return;
  }
  dlg.value = {
    item: fresh, phase: 'form',
    flavor: 'plain',
    backup: fresh.action === 'singbox-lx-replace' && (fresh.backupBytes || 0) > 0 && (fresh.freeBytes || 0) > (fresh.backupBytes || 0) + 64 * 1048576,
    ok: false, out: '',
  };
});

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
  void runInstall({ id: 'singbox-lx', flavor: dlg.value?.flavor || 'plain', backup: dlg.value?.backup || false });
}

function showResult(ok: boolean, output: string, errText: string): void {
  if (!dlg.value) return;
  dlg.value.ok = ok;
  dlg.value.out = (errText ? errText + '\n\n' : '') + output.trim();
  dlg.value.phase = 'result';
  void sys.refresh();
  if (ok) emit('installed');
}
</script>

<template>
  <UiDialog
    v-model:open="open"
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
        <div class="hrow">
          <UiSelect
            v-model="dlg.flavor"
            :options="flavorOptions"
            label="Профиль сборки"
          />
          <UiHelp :text="G.checksum" />
        </div>
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
        <UiButton @click="open = false">
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
        @click="open = false"
      >
        Закрыть
      </UiButton>
    </template>
  </UiDialog>
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
