<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiButton, UiDialog, UiHelp, UiHint, UiSelect, UiSwitch, type UiSelectOption } from '../../components/ui'
import { useSystem } from '../../stores/system'
import type { SingboxSourceInfo, SystemItem } from '../../api/types'
import { G } from '../glossary'

// Диалог установки ядра sing-box: источник релиза на выбор (extended - наш
// дефолт по слову владельца 2026-10-28, lx, апстрим), профиль сборки
// (plain/сжатый) и бэкап чужого ядра при замене. Общий для «Система ->
// Зависимости» и потока создания пула (слово владельца 2026-10-17: если
// движка нет - предлагать установку прямо при создании пула).
const open = defineModel<boolean>('open', { default: false });
const emit = defineEmits<{ installed: [] }>();

const sys = useSystem();

// фолбэк, если GitHub-дискавери недоступен: порядок = приоритет установки
const fallbackSources: SingboxSourceInfo[] = [
  { id: 'extended', title: 'sing-box-extended (форк shtorm-7)', note: 'форк shtorm-7: как lx, плюс новые протоколы; сжатые сборки под все платформы', upx: true, available: true },
  { id: 'lx', title: 'sing-box-lx (оригинал Leadaxe)', note: 'оригинальный lx-форк: самая свежая база, частые релизы, самое экономное по памяти', upx: true, available: true },
  { id: 'lx-own', title: 'sing-box-lx (наш форк)', note: 'наш форк: upx-сборки под все платформы (для тесной флешы x86)', upx: true, available: true },
  { id: 'upstream', title: 'sing-box (официальный апстрим)', note: 'официальный: без AWG-полей, xhttp и vless-шифрования', upx: false, available: true },
];

const dlg = ref<{
  item: SystemItem | null
  phase: 'form' | 'progress' | 'result'
  source: string
  flavor: string
  backup: boolean
  ok: boolean
  out: string
  sourceNote: string
  // что стояло при открытии: кнопка установки оживает только когда
  // выбрано изменение (слово владельца 2026-10-29)
  initSource: string
  initFlavor: string
} | null>(null);

const sources = ref<SingboxSourceInfo[]>([]);
const sourcesLoaded = ref(false);

const flavorOptions = computed<UiSelectOption[]>(() => {
  const src = sources.value.find((s) => s.id === dlg.value?.source);
  const opts: UiSelectOption[] = [
    { value: 'plain', label: 'обычный: быстрый старт, занимает больше места' },
  ];
  if (!src || src.upx) {
    opts.push({ value: 'upx', label: 'сжатый: в ~4 раза меньше на диске, старт дольше' });
  }
  return opts;
});

const sourceOptions = computed<UiSelectOption[]>(() =>
  sources.value.map((s) => {
    let hint = s.note;
    if (s.error) hint = `источник недоступен: ${s.error}`;
    else if (!s.available) hint = `${s.note} — нет сборки под архитектуру этого роутера`;
    else if (s.latest) hint = `${s.note} — свежий релиз ${s.latest}`;
    return { value: s.id, label: s.title, hint };
  }),
);

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
const dlgTitle = computed(() => (dlg.value?.item ? `${dlg.value.item.actionLabel || 'Установить'} - ядро sing-box` : ''));

// установка доступна, когда есть что делать: грузятся источники - нет;
// замена чужого/переустановка - всегда; обновление - сменился источник или
// профиль ЛИБО у выбранного источника вышел релиз новее установленного
const canInstall = computed(() => {
  const d = dlg.value;
  if (!d) return false;
  if (!sourcesLoaded.value) return false;
  if (d.item?.action !== 'singbox-lx') return true;
  if (d.source !== d.initSource || d.flavor !== d.initFlavor) return true;
  const latest = (sources.value.find((s) => s.id === d.source)?.latest || '').replace(/^v/, '');
  return latest !== '' && latest !== (d.item.version || '');
});
const installTooltip = computed(() =>
  sourcesLoaded.value
    ? 'изменений не выбрано: источник и профиль совпадают с установленным ядром, свежего релиза нет'
    : 'источники ещё загружаются с GitHub...',
);

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
  // источник: предвыбор установленному (чтобы «обновить» не меняло ядро
  // молча), иначе дефолт extended
  dlg.value = {
    item: fresh, phase: 'form',
    source: fresh.source || 'extended',
    flavor: 'plain',
    backup: fresh.action === 'singbox-lx-replace' && (fresh.backupBytes || 0) > 0 && (fresh.freeBytes || 0) > (fresh.backupBytes || 0) + 64 * 1048576,
    ok: false, out: '', sourceNote: '',
    initSource: fresh.source || 'extended', initFlavor: 'plain',
  };
  sourcesLoaded.value = false;
  // фолбэк сразу: селект никогда не «—» (слово владельца 2026-10-29:
  // «списка ядер первое время может не быть»); живой дискавери заменит
  // список, когда ответит
  sources.value = fallbackSources;
  try {
    const list = await sys.coreSources();
    if (list.length > 0) sources.value = list;
  } finally {
    sourcesLoaded.value = true;
  }
});

watch(() => dlg.value?.source, (id) => {
  // у апстрима нет сжатого профиля - сбрасываем на plain
  const src = sources.value.find((s) => s.id === id);
  if (dlg.value && dlg.value.flavor === 'upx' && src && !src.upx) {
    dlg.value.flavor = 'plain';
  }
});

async function runInstall(payload: { id: string; flavor?: string; source?: string; backup?: boolean }): Promise<void> {
  if (dlg.value) dlg.value.phase = 'progress';
  try {
    const r = await sys.install(payload);
    showResult(!!r.ok, r.output || '', r.ok ? '' : r.error || 'неизвестная ошибка', r.sourceTitle || r.source || '');
  } catch (e) {
    showResult(false, '', e instanceof Error ? e.message : String(e), '');
  }
}

function installForm(): void {
  void runInstall({
    id: 'singbox-lx',
    source: dlg.value?.source || '',
    flavor: dlg.value?.flavor || 'plain',
    backup: dlg.value?.backup || false,
  });
}

function showResult(ok: boolean, output: string, errText: string, sourceNote: string): void {
  if (!dlg.value) return;
  dlg.value.ok = ok;
  dlg.value.out = (errText ? errText + '\n\n' : '') + output.trim();
  dlg.value.sourceNote = sourceNote;
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
            v-model="dlg.source"
            :options="sourceOptions"
            label="Источник сборки"
          />
        </div>
        <div class="hrow">
          <UiSelect
            v-model="dlg.flavor"
            :options="flavorOptions"
            label="Профиль сборки"
          />
          <UiHelp :text="G.checksum" />
        </div>
        <div
          v-if="!sourcesLoaded"
          class="srcload"
        >
          <span class="spin" />
          источники уточняются (GitHub)...
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
        <div
          v-if="dlg.sourceNote"
          class="rssrc"
        >
          источник: {{ dlg.sourceNote }}
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
          :disabled="!canInstall"
          :tooltip="installTooltip"
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
.rssrc {
  font-size: 12.5px;
  color: var(--dim);
  margin-top: 2px;
}
.srcload {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12.5px;
  color: var(--dim);
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
