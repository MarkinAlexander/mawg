<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiBadge, UiButton, UiDialog, UiHint, UiInput } from '../../components/ui'
import { usePoolActions } from '../../stores/pools'
import { usePools } from '../../stores/status'
import { useToast } from '../../stores/toast'
import { nextFreeV4, occupiedV4, providerOf } from './poolsLogic'
import type { PoolView } from '../../api/types'

// Смена единого локального адреса всего пула (120-док, слово владельца
// 20.11: «в рамках пула конфиги ротируются - активен один, адрес общий;
// второй пул того же провайдера получает тот же адрес от провайдера -
// беру один раз и всем меняю, например на 10.2.0.3, и интерфейсы
// поднимаются вместе без коллизии»). Не +1 по конфигам: коллизия только
// МЕЖДУ пулами. Сервер валидирует занятость (исключая сам пул - его
// старый адрес освобождается) и переприменяет туннель.
const props = defineProps<{ pool: PoolView; open: boolean }>();
const emit = defineEmits<{ 'update:open': [value: boolean] }>();
const a = usePoolActions();
const { push } = useToast();
const { pools } = usePools();

const addr = ref('');
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

const v4ok = (s: string): boolean => {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(s.trim());
  if (!m) return false;
  return m.slice(1).every((o) => Number(o) <= 255);
};

// подсказка: первый свободный адрес подсети СРЕДИ ДРУГИХ пулов - свой
// адрес освобождается (второму протону предлагается 10.2.0.3, а не .6)
const suggestion = computed(() => {
  if (!provider.value) return '';
  const others = occupiedV4((pools.value || []).filter((p) => p.name !== props.pool.name));
  return nextFreeV4(others, provider.value.prefix);
});

// конфиги с IPv4 - только они несут локальный адрес; всем уходит ОДИН
const targets = computed(() => (props.pool.configs || []).filter((c) => v4of(c.addresses)));

const ok = computed(() => v4ok(addr.value));
const blocked = computed(() => !ok.value || busy.value);

watch(() => props.open, (v) => {
  if (v) addr.value = suggestion.value;
});

async function apply(): Promise<void> {
  if (blocked.value) return;
  busy.value = true;
  const a0 = addr.value.trim();
  const files = targets.value.map((c) => c.file);
  try {
    const r = await a.readdress(props.pool.name, {
      files,
      addresses: files.map(() => a0),
    });
    push('Сменено: весь пул на ' + a0 + ' (' + r.changed.length + ' конфигов)');
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
    width="500px"
    @update:open="emit('update:open', $event)"
  >
    <UiHint
      :text="provider
        ? 'Похоже на ' + provider.label + ' (' + provider.prefix + 'x). Весь пул получает ОДИН адрес: внутри пула конфиги ротируются - активен один, коллизия только между пулами. Второму пулу провайдера достаточно первого свободного адреса (для протона обычно 10.2.0.3)'
        : 'Весь пул получает ОДИН локальный адрес: внутри пула конфиги ротируются - активен один, коллизия только между пулами. Укажите свободный адрес'"
    />
    <div class="rrow">
      <UiInput
        v-model="addr"
        label="Новый адрес пула"
        :placeholder="suggestion || '10.2.0.3'"
        mono
        class="rin"
        :validator="() => addr.trim() && !v4ok(addr.trim()) ? 'адрес - IPv4 вида 10.2.0.3' : null"
      />
      <UiButton
        v-if="suggestion"
        variant="ghost"
        :disabled="busy"
        tooltip="первый свободный адрес подсети среди других пулов"
        @click="addr = suggestion"
      >
        {{ suggestion }}
      </UiButton>
    </div>
    <div class="rlist">
      <div
        v-for="c in targets"
        :key="c.file"
        class="rrow-item"
      >
        <span class="rname">{{ c.original || c.file }}<UiBadge
          v-if="!c.enabled"
          text="выкл"
          variant="muted"
        /></span>
        <span class="raddr">{{ v4of(c.addresses) || '?' }} → {{ addr.trim() || '?' }}</span>
      </div>
    </div>
    <UiHint text="Новый адрес при сохранении проверяется на занятость другими пулами и поднятыми туннелями - этот пул и его старый адрес не считаются. Занят - сохранение отклонится со списком конфликтов, ничего не сменится; свободен - адрес получат все конфиги пула, туннель переприменится." />
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
        :disabled="blocked"
        :busy="busy"
        @click="apply"
      >
        Сменить IP пула
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
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  font-size: 12.5px;
  padding: 3px 0;
  border-bottom: 1px solid var(--border);
}
.rrow-item:last-child {
  border-bottom: 0;
}
.rname {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.raddr {
  color: var(--dim);
  white-space: nowrap;
}
</style>
