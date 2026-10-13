<script setup lang="ts">
import UiHint from './UiHint.vue'

// Тумблер с рядом полей (просьба владельца: «каждый свитч с рядом полей»):
// подпись + hint слева, свитч 32x18 справа - разметка идентична legacy
// (.switch/.knob). tooltip - в title всей строки. busy гасит клики.
// Без label - «голый» тумблер (для строк таблиц/чипов, как bare-режим
// UiCheckbox); семантика включено/выключено (--ok), не «выбрано».
const props = withDefaults(
  defineProps<{
    modelValue: boolean
    label?: string
    hint?: string
    tooltip?: string
    disabled?: boolean
    busy?: boolean
    // tight: свитч вплотную к подписи, а не у правого края строки
    tight?: boolean
  }>(),
  { label: undefined, hint: undefined, tooltip: undefined, tight: false },
)

const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()

function onChange(e: Event) {
  emit('update:modelValue', (e.target as HTMLInputElement).checked)
}
</script>

<template>
  <div
    class="uiswitch"
    :class="{ off: props.disabled || props.busy, bare: !props.label, tight: props.tight }"
    :title="props.tooltip"
  >
    <span
      v-if="props.label"
      class="text"
    >
      <span class="lbl">{{ props.label }}</span>
      <UiHint
        v-if="props.hint"
        :text="props.hint"
      />
      <slot name="hint-extra" />
    </span>
    <label class="switch">
      <input
        type="checkbox"
        :checked="props.modelValue"
        :disabled="props.disabled || props.busy"
        :aria-label="props.label || props.tooltip || 'переключатель'"
        @change="onChange"
      >
      <span class="knob" />
    </label>
  </div>
</template>

<style scoped>
.uiswitch {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 8px 0;
  font-size: 13px;
}
.uiswitch .text {
  flex: 1;
}
.uiswitch .lbl {
  color: var(--text);
}
.uiswitch.off {
  opacity: 0.55;
}
.uiswitch.off .switch {
  pointer-events: none;
}
/* голый режим: только тумблер, для чипов/строк таблиц */
.uiswitch.bare {
  display: inline-flex;
  margin: 0;
}
/* свитч вплотную к подписи (по умолчанию - у правого края строки) */
.uiswitch.tight {
  justify-content: flex-start;
  gap: 8px;
}
.uiswitch.tight .text {
  flex: none;
}
/* Точная копия legacy .switch/.knob (panel.css) */
.switch {
  position: relative;
  display: inline-block;
  width: 32px;
  height: 18px;
  vertical-align: middle;
  flex: none;
}
.switch input {
  opacity: 0;
  width: 0;
  height: 0;
}
.switch .knob {
  position: absolute;
  cursor: pointer;
  inset: 0;
  background: var(--border);
  border-radius: 10px;
  transition: 0.15s;
}
.switch .knob:before {
  content: '';
  position: absolute;
  height: 12px;
  width: 12px;
  left: 3px;
  top: 3px;
  background: var(--bg);
  border-radius: 50%;
  transition: 0.15s;
}
.switch input:checked + .knob {
  background: var(--ok);
}
.switch input:checked + .knob:before {
  transform: translateX(14px);
}
</style>
