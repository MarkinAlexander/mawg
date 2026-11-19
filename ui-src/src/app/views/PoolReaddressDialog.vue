<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiBadge, UiButton, UiCheckbox, UiDialog, UiHint, UiInput } from '../../components/ui'
import { usePoolActions } from '../../stores/pools'
import { usePools } from '../../stores/status'
import { useToast } from '../../stores/toast'
import { nextFreeV4, occupiedV4, providerOf } from './poolsLogic'
import type { PoolView } from '../../api/types'

// Перенумерация/смена локальных адресов конфигов пула (120-док, слово
// владельца 15.11 «массово менять в них адрес локальный» + 20.11 «менять
// прям на этапе загрузки, как по одному так и массово»): однотипная куча
// (Proton 10.2.0.x, WARP 172.16.0.x) приходит с ОДНИМ адресом - два таких
// пула одновременно не поднимутся. База генерирует план (+1 к последнему
// октету), каждый адрес можно поправить руками и снять галочку - уйдут
// только выбранные строки (сервер валидирует занятость и переприменяет).
const props = defineProps<{ pool: PoolView; open: boolean }>();
const emit = defineEmits<{ 'update:open': [value: boolean] }>();
const a = usePoolActions();
const { push } = useToast();
const { pools } = usePools();

interface Row {
  file: string;
  name: string;
  off: boolean;
  old: string;
  addr: string;
  checked: boolean;
}

const base = ref('');
const busy = ref(false);
const rows = ref<Row[]>([]);

// Провайдер по первому опознанному конфигу: подпись + подсеть подсказки
const provider = computed(() => {
  for (const c of props.pool.configs || []) {
    const p = providerOf(c.addresses);
    if (p) return p;
  }
  return null;
});

const v4of = (addresses?: string[]): string =>
  (addresses || []).find((x) => !x.includes(':'))?.split('/')[0] || '';

// адрес без префикса - префикс прежний сохранит сервер (SetAddress)
const v4ok = (s: string): boolean => {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(s.trim());
  if (!m) return false;
  return m.slice(1).every((o) => Number(o) <= 255);
};

const suggestion = computed(() => {
  if (!provider.value) return '';
  return nextFreeV4(occupiedV4(pools.value), provider.value.prefix);
});

// план по базе: i-му конфигу последний октет +i; база невалидна -
// адреса строк не трогаем (ручные правки остаются)
function refill(): void {
  const m = base.value.trim().match(/^(\d{1,3}\.\d{1,3}\.\d{1,3})\.(\d{1,3})$/);
  if (!m) return;
  const first = Number(m[2]);
  rows.value.forEach((r, i) => {
    r.addr = first + i <= 255 ? `${m[1]}.${first + i}` : '';
  });
}

function rebuild(): void {
  rows.value = (props.pool.configs || []).map((c) => ({
    file: c.file,
    name: c.original || c.file,
    off: !c.enabled,
    old: v4of(c.addresses),
    addr: '',
    checked: true,
  }));
  refill();
}

watch(() => props.open, (v) => {
  if (v) {
    base.value = suggestion.value;
    rebuild();
  }
});

// уйдут только выбранные строки с валидным непустым адресом
const chosen = computed(() =>
  rows.value.filter((r) => r.checked && r.addr && v4ok(r.addr)));
const blocked = computed(() =>
  rows.value.some((r) => r.checked && r.addr && !v4ok(r.addr)) || !chosen.value.length);

async function apply(): Promise<void> {
  if (busy.value || blocked.value) return;
  busy.value = true;
  try {
    const r = await a.readdress(props.pool.name, {
      files: chosen.value.map((x) => x.file),
      addresses: chosen.value.map((x) => x.addr.trim()),
    });
    push('Сменено: ' + r.changed.map((c) => c.file + ' → ' + c.address).join(', '));
    for (const w of r.warnings || []) push(w, true);
    emit('update:open', false);
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  busy.value = false;
}
</script>

<template>
  <UiDialog
    :open="open"
    :title="'Локальные адреса - ' + pool.name"
    :busy="busy"
    busy-text="идёт смена адресов…"
    width="520px"
    @update:open="emit('update:open', $event)"
  >
    <UiHint
      :text="provider
        ? 'Похоже на ' + provider.label + ' (' + provider.prefix + 'x): одинаковые адреса в нескольких пулах не поднимутся одновременно - разведите их одной подсетью. База задает план (+1 каждому), адрес можно поправить руками, галочка снимает строку'
        : 'Одинаковые локальные адреса в нескольких пулах не поднимутся одновременно: задайте базу (план +1 каждому), поправьте адреса руками или снимите галочки - уйдут только выбранные строки'"
    />
    <div class="rrow">
      <UiInput
        v-model="base"
        label="Базовый адрес"
        :placeholder="suggestion || '10.2.0.2'"
        mono
        class="rin"
        @update:model-value="refill"
      />
      <UiButton
        v-if="suggestion"
        variant="ghost"
        :disabled="busy"
        tooltip="первый свободный адрес подсети"
        @click="base = suggestion; refill()"
      >
        {{ suggestion }}
      </UiButton>
    </div>
    <div class="rlist">
      <div
        v-for="r in rows"
        :key="r.file"
        class="rrow-item"
        :class="{ bad: r.checked && r.addr && !v4ok(r.addr), skip: !r.checked }"
      >
        <UiCheckbox
          :model-value="r.checked"
          @update:model-value="(v: boolean) => (r.checked = v)"
        />
        <span class="rname">{{ r.name }}<UiBadge
          v-if="r.off"
          text="выкл"
          variant="muted"
        /></span>
        <span class="raddr">{{ r.old || '?' }} →</span>
        <UiInput
          v-model="r.addr"
          bare
          mono
          class="rinput"
          :placeholder="r.old ? r.old.split('/')[0] : 'адрес'"
        />
      </div>
    </div>
    <UiHint text="Пустой адрес = строка пропускается. Занятость и валидность перепроверит сервер (у него виднее живые туннели)." />
    <template #footer>
      <UiButton
        variant="ghost"
        :disabled="busy"
        @click="emit('update:open', false)"
      >
        Отмена
      </UiButton>
      <UiButton
        variant="primary"
        :disabled="busy || blocked"
        :busy="busy"
        @click="apply"
      >
        Сменить адреса
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.rrow {
  display: flex;
  align-items: end;
  gap: 8px;
  margin-top: 10px;
}
.rin {
  flex: 1;
}
.rlist {
  margin-top: 10px;
  display: grid;
  gap: 2px;
  max-height: 260px;
  overflow-y: auto;
}
.rrow-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12.5px;
  padding: 3px 0;
  border-bottom: 1px solid var(--border);
}
.rrow-item:last-child {
  border-bottom: 0;
}
.rrow-item.bad {
  color: var(--warn);
}
.rrow-item.skip {
  opacity: 0.45;
}
.rname {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.raddr {
  color: var(--dim);
  white-space: nowrap;
}
.rinput {
  width: 120px;
}
</style>
