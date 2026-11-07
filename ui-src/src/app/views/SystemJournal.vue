<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { UiCard, UiInput, UiSelect } from '../../components/ui'
import { useEvents } from '../../stores/events'
import { useSecrets } from '../secrets'
import type { AppEvent } from '../../api/types'
import { evFilteredOf, evKindOptionsOf, fmtTime, kindRu } from './systemLogic'

// Карточка «Журнал событий» вкладки «Система»: фильтры + хвост 80.
// Чистые функции (лейблы kinds, фильтрация, время) - в systemLogic.ts.
// Журнал событий (легаси refreshLog): место - вкладка «Система» (44-док §6).
// Опрос таб-локальный: start/stop на монтировании вьюхи, интервал 15с как легаси.
const { state, start, stop } = useEvents();
// приватный режим: строка журнала размыта, только если в ней адрес из конфигов
const secrets = useSecrets();

onMounted(() => {
  start();
});
onBeforeUnmount(() => {
  stop();
});

// UiCard - контролируемый: без v-model свёртывание не работает
const collapsed = ref(false);

// Ф5 §7 «страница событий с фильтрами»: тип события из данных, не
// захардкожен; фильтр - по всему списку, хвост 80 - уже отфильтрованного.
const evKind = ref('');
const evQuery = ref('');
const evKindOptions = computed(() => evKindOptionsOf(state.list));
const evFiltered = computed<AppEvent[]>(() => evFilteredOf(state.list, evKind.value, evQuery.value));
const evFiltering = computed(() => !!evKind.value || !!evQuery.value.trim());
</script>

<template>
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
</style>
