<script setup lang="ts">
import { computed } from 'vue'

// Бейдж статуса (legacy .badge up/fallback/cool + инлайн-перекраски).
// tooltip идёт в title (cursor: help).
const props = defineProps<{
  text: string
  variant?: 'ok' | 'err' | 'warn' | 'info' | 'muted'
  tooltip?: string
}>()

const variant = computed(() => props.variant ?? 'muted')
</script>

<template>
  <span
    class="uibadge"
    :class="`v-${variant}`"
    :title="props.tooltip"
  >{{ props.text }}</span>
</template>

<style scoped>
.uibadge {
  padding: 1px 8px;
  border-radius: 10px;
  font-size: 11px;
  border: 1px solid var(--border);
  white-space: nowrap;
}
.v-ok {
  color: var(--ok);
  border-color: var(--ok);
}
.v-err {
  color: var(--err);
  border-color: var(--err);
}
.v-warn {
  color: var(--warn);
  border-color: var(--warn);
}
.v-info {
  color: var(--accent);
  border-color: var(--accent);
}
.v-muted {
  color: var(--dim);
  border-color: var(--border);
}
</style>
