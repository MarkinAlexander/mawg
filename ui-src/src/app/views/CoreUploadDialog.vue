<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiBadge, UiButton, UiDialog, UiHelp, UiHint, UiSwitch } from '../../components/ui'
import { useSystem } from '../../stores/system'
import type { UploadAnalyzeRes } from '../../api/types'

// Диалог загрузки бинаря/пакета движка (112-док 4.1, слово владельца
// 04.11: «понимать что пользователь ставит и что загружает»): замена
// ядер sing-box/mihomo/MT без GitHub; панель сама распознаёт файл
// (бинарь: ELF + version-запуск, архитектура против роутера; .ipk/.apk
// MT), показывает, что меняет, замена по подтверждению.
const open = defineModel<boolean>('open', { default: false });
const emit = defineEmits<{ installed: [] }>();

const sys = useSystem();

const dlg = ref<{
  file: File | null
  backup: boolean
  phase: 'form' | 'analyze' | 'confirm' | 'apply' | 'result'
  detected: UploadAnalyzeRes | null
  ok: boolean
  out: string
} | null>(null);

const mb = (b: number) => (b / 1048576).toFixed(0) + ' МБ';
const fileNote = computed(() => {
  const f = dlg.value?.file;
  return f ? `${f.name} (${mb(f.size)})` : '';
});
const canAnalyze = computed(() => !!dlg.value?.file && dlg.value.file.size > 200_000);
const analyzeTooltip = computed(() => {
  const f = dlg.value?.file;
  if (!f) return 'выберите файл';
  if (f.size <= 200_000) return 'файл слишком мал: бинарь весит мегабайты, пакет MT - сотни килобайт';
  return '';
});
const isMT = computed(() => dlg.value?.detected?.kind === 'magitrickle');

watch(open, (v) => {
  if (!v) return;
  dlg.value = { file: null, backup: true, phase: 'form', detected: null, ok: false, out: '' };
});

function onFile(e: Event): void {
  const input = e.target as HTMLInputElement;
  if (dlg.value) {
    dlg.value.file = input.files?.[0] || null;
    dlg.value.detected = null;
  }
}

// фаза 1: загрузка + детект (ничего не меняется)
async function analyze(): Promise<void> {
  const d = dlg.value;
  if (!d || !d.file) return;
  d.phase = 'analyze';
  try {
    const r = await sys.analyzeCoreBinary(d.file);
    if (!r.ok) {
      d.ok = false;
      d.out = r.error || 'не распознано';
      d.phase = 'result';
      return;
    }
    d.detected = r;
    d.phase = 'confirm';
  } catch (e) {
    d.ok = false;
    d.out = e instanceof Error ? e.message : String(e);
    d.phase = 'result';
  }
}

// фаза 2: применение по подтверждению
async function apply(): Promise<void> {
  const d = dlg.value;
  if (!d) return;
  d.phase = 'apply';
  try {
    const r = await sys.applyCoreBinary(d.backup);
    d.ok = !!r.ok;
    d.out = r.output || r.error || '';
  } catch (e) {
    d.ok = false;
    d.out = e instanceof Error ? e.message : String(e);
  }
  d.phase = 'result';
  if (d.ok) {
    emit('installed');
    void sys.refreshCores();
    void sys.refresh();
  }
}
</script>

<template>
  <UiDialog
    v-model:open="open"
    title="Загрузить бинарь движка"
    :busy="dlg?.phase === 'analyze' || dlg?.phase === 'apply'"
    :locked="dlg?.phase === 'analyze' || dlg?.phase === 'apply'"
    width="520px"
  >
    <template v-if="dlg">
      <template v-if="dlg.phase === 'form'">
        <div class="lxtext">
          Для роутеров без доступа к GitHub или где установка из архива не проходит по месту:
          скачайте файл кнопкой «Скачать» (рядом), при необходимости распакуйте на компьютере
          (tar.gz / gz) и залейте сюда. Панель сама распознает, что это - бинарь sing-box,
          бинарь mihomo или пакет MagiTrickle (.ipk/.apk) - и покажет перед заменой.
        </div>
        <div class="hrow">
          <input
            type="file"
            class="fileinp"
            @change="onFile"
          >
          <UiHelp text="сам бинарь (не архив) или пакет .ipk/.apk MagiTrickle" />
        </div>
        <UiHint
          v-if="fileNote"
          :text="fileNote"
        />
      </template>
      <div
        v-else-if="dlg.phase === 'analyze'"
        class="progress"
      >
        Файл загружается и распознаётся...
      </div>
      <template v-else-if="dlg.phase === 'confirm' && dlg.detected">
        <div class="detected">
          Распознано:
          <b>{{ dlg.detected.label }}</b>
          <UiBadge
            v-if="dlg.detected.arch"
            :text="`архитектура ${dlg.detected.arch}`"
            :variant="dlg.detected.arch === dlg.detected.routerArch ? 'ok' : 'warn'"
            :tooltip="`роутер: ${dlg.detected.routerArch || '?'}`"
          />
        </div>
        <UiHint :text="dlg.detected.hint || ''" />
        <UiSwitch
          v-if="dlg.detected.needsBackup"
          :model-value="dlg.backup"
          label="сохранить копию старого бинаря (откат при неудачном старте)"
          tight
          @update:model-value="(v: boolean) => dlg && (dlg.backup = v)"
        />
      </template>
      <div
        v-else-if="dlg.phase === 'apply'"
        class="progress"
      >
        {{ isMT ? 'Пакет устанавливается, служба MagiTrickle перезапускается...' : 'Движок останавливается, бинарь заменяется, ядро запускается и проверяется...' }}
      </div>
      <template v-else>
        <div
          class="rstitle"
          :class="dlg.ok ? 'ok' : 'err'"
        >
          {{ dlg.ok ? 'Готово' : 'Не удалось' }}
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
          :disabled="!canAnalyze"
          :tooltip="analyzeTooltip"
          @click="analyze"
        >
          Загрузить
        </UiButton>
      </template>
      <template v-else-if="dlg?.phase === 'confirm'">
        <UiButton @click="open = false">
          Отмена
        </UiButton>
        <UiButton
          variant="primary"
          @click="apply"
        >
          {{ isMT ? 'Установить пакет' : 'Заменить' }}
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
.lxtext {
  color: var(--dim);
  font-size: 12px;
  line-height: 1.5;
  margin-bottom: 10px;
}
.hrow {
  display: flex;
  gap: 6px;
  align-items: center;
  margin-top: 4px;
}
.fileinp {
  flex: 1;
  min-width: 0;
  font-size: 12px;
}
.progress {
  color: var(--dim);
  font-size: 12px;
  line-height: 1.5;
}
.detected {
  font-size: 13px;
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
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
  margin-top: 6px;
}
</style>
