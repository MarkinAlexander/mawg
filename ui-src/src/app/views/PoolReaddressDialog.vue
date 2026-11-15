<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiBadge, UiButton, UiDialog, UiHint, UiInput } from '../../components/ui'
import { usePoolActions } from '../../stores/pools'
import { usePools } from '../../stores/status'
import { useToast } from '../../stores/toast'
import { nextFreeV4, occupiedV4, providerOf } from './poolsLogic'
import type { PoolView } from '../../api/types'

// Перенумерация локальных адресов конфигов пула (120-док, слово
// владельца 15.11 «массово менять в них адрес локальный»): однотипная
// куча (Proton 10.2.0.x, WARP 172.16.0.x) приходит с ОДНИМ адресом -
// два таких пула одновременно не поднимутся. Один ход: base первому
// конфигу, дальше +1 к последнему октету; занятость и валидность
// перепроверяет сервер (у него виднее живые туннели).
const props = defineProps<{ pool: PoolView; open: boolean }>();
const emit = defineEmits<{ 'update:open': [value: boolean] }>();
const a = usePoolActions();
const { push } = useToast();
const { pools } = usePools();

const base = ref('');
const busy = ref(false);

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

// Перенумеруется весь пул: свежая куча однотипных выключена конфликтом
// адресов ещё до ротации - по включённым её не достать. Выключенные
// помечаем, чтобы факт переезда адреса был виден.
const targets = computed(() => props.pool.configs || []);

const suggestion = computed(() => {
  if (!provider.value) return '';
  return nextFreeV4(occupiedV4(pools.value), provider.value.prefix);
});

// база «10.2.0.5» -> каждому следующему +1; хвост вне /24 - конфиг в
// превью не получает адрес, применение блокировано
const preview = computed(() => {
  const m = base.value.trim().match(/^(\d{1,3}\.\d{1,3}\.\d{1,3})\.(\d{1,3})$/);
  if (!m) return [];
  const first = Number(m[2]);
  return targets.value.map((c, i) => ({
    file: c.file,
    name: c.original || c.file,
    off: !c.enabled,
    old: v4of(c.addresses),
    addr: first + i <= 255 ? `${m[1]}.${first + i}` : '',
  }));
});
const overflow = computed(() => preview.value.some((p) => !p.addr));

watch(() => props.open, (v) => {
  if (v) base.value = suggestion.value;
});

async function apply(): Promise<void> {
  if (busy.value || !base.value.trim() || overflow.value) return;
  busy.value = true;
  try {
    const r = await a.readdress(props.pool.name, base.value.trim());
    push('Перенумеровано: ' + r.changed.map((c) => c.file + ' → ' + c.address).join(', '));
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
    busy-text="идёт перенумерация…"
    width="480px"
    @update:open="emit('update:open', $event)"
  >
    <UiHint
      :text="provider
        ? 'Похоже на ' + provider.label + ' (' + provider.prefix + 'x): одинаковые адреса в нескольких пулах не поднимутся одновременно - перенумеруйте их одной подсетью'
        : 'Одинаковые локальные адреса в нескольких пулах не поднимутся одновременно: задайте базу, каждому следующему конфигу последний октет +1'"
    />
    <div class="rrow">
      <UiInput
        v-model="base"
        label="Базовый адрес"
        :placeholder="suggestion || '10.2.0.2'"
        mono
        class="rin"
      />
      <UiButton
        v-if="suggestion"
        variant="ghost"
        :disabled="busy"
        tooltip="первый свободный адрес подсети"
        @click="base = suggestion"
      >
        {{ suggestion }}
      </UiButton>
    </div>
    <div class="rlist">
      <div
        v-for="p in preview"
        :key="p.file"
        class="rrow-item"
        :class="{ bad: !p.addr }"
      >
        <span class="rname">{{ p.name }}<UiBadge
          v-if="p.off"
          text="выкл"
          variant="muted"
        /></span>
        <span class="raddr">
          {{ p.old || '?' }} → {{ p.addr || 'не влезает в /24' }}
        </span>
      </div>
    </div>
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
        :disabled="busy || !base.trim() || overflow"
        :busy="busy"
        @click="apply"
      >
        Перенумеровать
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
  max-height: 240px;
  overflow-y: auto;
}
.rrow-item {
  display: flex;
  justify-content: space-between;
  gap: 10px;
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
.rname {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.raddr {
  color: var(--dim);
  white-space: nowrap;
}
</style>
