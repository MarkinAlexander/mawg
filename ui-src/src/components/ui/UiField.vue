<script setup lang="ts">
import { useId } from 'vue'
import UiHint from './UiHint.vue'

// Обёртка «подпись + контрол + серый hint + ошибка» (44-дока §4).
// tooltip идёт в title подписи. Контрол получает id через slot props
// (для связки label[for]); внутренние компоненты кита bind'ят сами.
const props = defineProps<{
  label: string
  hint?: string
  tooltip?: string
  required?: boolean
  error?: string
}>()

const id = useId()
</script>

<template>
  <div class="uifield">
    <label
      class="lbl"
      :for="id"
      :title="props.tooltip"
    >
      {{ props.label }}
      <span
        v-if="props.required"
        class="req"
        title="обязательное"
      >*</span>
    </label>
    <slot :id="id" />
    <UiHint
      v-if="props.hint"
      :text="props.hint"
    />
    <div
      v-if="props.error"
      class="err"
      role="alert"
    >
      {{ props.error }}
    </div>
  </div>
</template>

<style scoped>
.uifield {
  display: block;
  margin: 8px 0;
  font-size: 13px;
}
.lbl {
  display: block;
  color: var(--dim);
  cursor: pointer;
}
.req {
  color: var(--warn);
  margin-left: 2px;
}
.err {
  color: var(--err);
  font-size: 11px;
  margin-top: 3px;
}
</style>
