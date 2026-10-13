<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { UiCard } from '../../components/ui'
import { useEvents } from '../../stores/events'
import type { AppEvent } from '../../api/types'

// Журнал событий (легаси refreshLog): хвост 80, НОВЫЕ СВЕРХУ, k-цвета
// пяти видов. Место - вкладка «Система» (44-дока §6). Опрос таб-локальный:
// start/stop на монтировании вьюхи, интервал 15с как легаси.
const { state, start, stop } = useEvents();

onMounted(start);
onBeforeUnmount(stop);

// UiCard - контролируемый: без v-model свёртывание не работает
const collapsed = ref(false);

const tail = computed<AppEvent[]>(() => state.list.slice(-80).reverse());

// легаси fmtTime: toLocaleTimeString('ru-RU')
function fmtTime(iso: string): string {
  return iso ? new Date(iso).toLocaleTimeString('ru-RU') : '';
}
</script>

<template>
  <UiCard
    v-model:collapsed="collapsed"
    title="Журнал событий"
    subtitle="последние 80 записей, новые сверху"
    collapsible
  >
    <div class="log">
      <div
        v-for="(ev, i) in tail"
        :key="i"
      >
        [{{ fmtTime(ev.time) }}] {{ ev.pool }}:
        <span :class="'k-' + ev.kind">{{ ev.kind }}</span>
        {{ ev.message }}
      </div>
    </div>
  </UiCard>
</template>

<style scoped>
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
