<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiButton, UiDialog, UiHelp, UiHint, UiSwitch } from '../../components/ui'
import { useSystem } from '../../stores/system'
import type { CoreInfo } from '../../api/types'

// Диалог заливки своего бинаря движка (112-док 4.1, слово владельца
// 04.11: на забитых роутерах установка tar.gz ломается на распаковке -
// распакуйте архив на своём ПК и залите готовый бинарь). Панель сама
// кладёт куда надо: ELF/arch-проверка, права, откат на копию старого.
const open = defineModel<boolean>('open', { default: false });
const emit = defineEmits<{ installed: [] }>();

const sys = useSystem();

const dlg = ref<{
  core: CoreInfo | null
  file: File | null
  backup: boolean
  phase: 'form' | 'progress' | 'result'
  ok: boolean
  out: string
} | null>(null);

const coreTitle = computed(() => dlg.value?.core?.title || '');
const mb = (b: number) => (b / 1048576).toFixed(0) + ' МБ';
const fileNote = computed(() => {
  const f = dlg.value?.file;
  if (!f) return '';
  return `файл: ${f.name} (${mb(f.size)})`;
});

// заливать можно когда выбран файл; бинарь обязан быть мегабайтным -
// огрызок сразу виден
const canUpload = computed(() => !!dlg.value?.file && dlg.value.file.size > 2 * 1048576);
const uploadTooltip = computed(() => {
  const f = dlg.value?.file;
  if (!f) return 'выберите файл бинаря';
  if (f.size <= 2 * 1048576) return 'файл слишком мал: бинарь движка весит мегабайты (это точно распакованный бинарь, не архив?)';
  return '';
});

watch(open, (v) => {
  if (!v) return;
  dlg.value = { core: null, file: null, backup: true, phase: 'form', ok: false, out: '' };
});

function openFor(core: CoreInfo): void {
  if (dlg.value) dlg.value.core = core;
}

defineExpose({ openFor });

function onFile(e: Event): void {
  const input = e.target as HTMLInputElement;
  if (dlg.value) dlg.value.file = input.files?.[0] || null;
}

async function upload(): Promise<void> {
  const d = dlg.value;
  if (!d || !d.file || !d.core) return;
  d.phase = 'progress';
  try {
    const r = await sys.uploadCoreBinary(d.core.id, d.file, d.backup);
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
  }
}
</script>

<template>
  <UiDialog
    v-model:open="open"
    :title="`Загрузить бинарь - ${coreTitle}`"
    :busy="dlg?.phase === 'progress'"
    :locked="dlg?.phase === 'progress'"
    width="520px"
  >
    <template v-if="dlg">
      <template v-if="dlg.phase === 'form'">
        <div class="lxtext">
          Для роутеров, где установка из архива не проходит из-за места: скачайте релизный архив
          на свой компьютер (кнопка «Скачать бинарь» в карточке ядра), распакуйте и залейте
          готовый бинарь здесь. Панель проверит архитектуру, остановит движок, заменит бинарь,
          запустит и при неудаче вернёт прежний из копии.
        </div>
        <div class="hrow">
          <input
            type="file"
            class="fileinp"
            @change="onFile"
          >
          <UiHelp text="сам бинарь (sing-box / mihomo), не архив: tar.gz распакуйте на ПК, gz - gunzip" />
        </div>
        <UiHint
          v-if="fileNote"
          :text="fileNote"
        />
        <UiSwitch
          :model-value="dlg.backup"
          label="сохранить копию старого бинаря (откат при неудачном старте)"
          tight
          @update:model-value="(v: boolean) => dlg && (dlg.backup = v)"
        />
        <UiHint text="Замена останавливает движок на секунды: его движковые пулы коротко прервутся. Без копии старого при неудачной замене движок останется сломанным - залить рабочий бинарь можно будет той же кнопкой." />
      </template>
      <div
        v-else-if="dlg.phase === 'progress'"
        class="progress"
      >
        Бинарь загружается и ставится: проверка архитектуры, остановка движка, замена, старт
        и проба. Это занимает до пары минут - не закрывайте панель.
      </div>
      <template v-else>
        <div
          class="rstitle"
          :class="dlg.ok ? 'ok' : 'err'"
        >
          {{ dlg.ok ? 'Бинарь заменён' : 'Замена не удалась' }}
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
          :disabled="!canUpload"
          :tooltip="uploadTooltip"
          @click="upload"
        >
          Заменить бинарь
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
