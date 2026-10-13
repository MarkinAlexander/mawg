<script setup lang="ts">
import { computed } from 'vue'

// Кнопка кита (legacy button/.primary/.danger/.iconbtn). busy ставит
// спиннер-псевдоэлемент и disabled (семантика withBusy: двойной запуск
// невозможен, клик не проходит). tooltip - в title.
const props = defineProps<{
  variant?: 'ghost' | 'primary' | 'danger' | 'icon'
  busy?: boolean
  disabled?: boolean
  tooltip?: string
  type?: 'button' | 'submit'
}>()

const emit = defineEmits<{ click: [event: MouseEvent] }>()

const isBusy = computed(() => props.busy ?? false)
const isDisabled = computed(() => props.disabled || isBusy.value)
</script>

<template>
  <button
    class="uibtn"
    :class="[`v-${props.variant ?? 'ghost'}`, { busy: isBusy }]"
    :type="props.type ?? 'button'"
    :disabled="isDisabled"
    :title="props.tooltip"
    @click="emit('click', $event)"
  >
    <slot />
  </button>
</template>

<style scoped>
.uibtn {
  background: var(--panel2);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 5px 10px;
  font-size: 12px;
  cursor: pointer;
}
.uibtn:hover:not(:disabled) {
  border-color: var(--accent);
}
.uibtn:disabled {
  opacity: 0.55;
  cursor: default;
}
.uibtn.v-primary {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
}
.uibtn.v-danger:hover:not(:disabled) {
  border-color: var(--err);
  color: var(--err);
}
.uibtn.v-icon {
  background: none;
  border-color: transparent;
  color: var(--dim);
  padding: 3px;
  border-radius: 5px;
  vertical-align: middle;
}
.uibtn.v-icon:hover:not(:disabled) {
  color: var(--err);
}
.uibtn.busy {
  pointer-events: none;
}
.uibtn.busy::before {
  content: '';
  display: inline-block;
  width: 11px;
  height: 11px;
  border: 2px solid currentColor;
  border-right-color: transparent;
  border-radius: 50%;
  margin-right: 7px;
  vertical-align: -2px;
  animation: uibtn-spin 0.7s linear infinite;
}
@keyframes uibtn-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
