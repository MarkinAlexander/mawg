<script setup lang="ts">
import UiHint from './UiHint.vue'

// Чекбокс кита: выбор/отметка (в отличие от свитча-«включено»). Два
// режима: с label/hint - раскладка как у UiSwitch (подпись слева, бокс
// справа); без label - «голый» бокс для строк таблиц и компактных мест.
// Отметка - синий (accent), чтобы не путать со свитчом (--ok).
const props = withDefaults(
  defineProps<{
    modelValue: boolean
    label?: string
    hint?: string
    tooltip?: string
    disabled?: boolean
    busy?: boolean
  }>(),
  { label: undefined, hint: undefined, tooltip: undefined },
)

const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()

function onChange(e: Event) {
  emit('update:modelValue', (e.target as HTMLInputElement).checked)
}
</script>

<template>
  <label
    v-if="!props.label"
    class="uicheckbox"
    :class="{ off: props.disabled || props.busy }"
    :title="props.tooltip"
  >
    <input
      type="checkbox"
      :checked="props.modelValue"
      :disabled="props.disabled || props.busy"
      :aria-label="props.tooltip"
      @change="onChange"
    >
    <span class="box" />
  </label>
  <div
    v-else
    class="uicheckbox row"
    :class="{ off: props.disabled || props.busy }"
    :title="props.tooltip"
  >
    <span class="text">
      <span class="lbl">{{ props.label }}</span>
      <UiHint
        v-if="props.hint"
        :text="props.hint"
      />
    </span>
    <label class="check">
      <input
        type="checkbox"
        :checked="props.modelValue"
        :disabled="props.disabled || props.busy"
        :aria-label="props.label"
        @change="onChange"
      >
      <span class="box" />
    </label>
  </div>
</template>

<style scoped>
.uicheckbox {
  display: inline-block;
  font-size: 13px;
}
.uicheckbox.row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  width: 100%;
  margin: 8px 0;
}
.uicheckbox.row .text {
  flex: 1;
}
.uicheckbox.row .lbl {
  color: var(--text);
}
.uicheckbox.off {
  opacity: 0.55;
}
.uicheckbox.off .check,
.uicheckbox.off input {
  pointer-events: none;
}
.check {
  display: inline-block;
  flex: none;
  vertical-align: middle;
}
input {
  opacity: 0;
  width: 0;
  height: 0;
}
.box {
  position: relative;
  display: inline-block;
  width: 15px;
  height: 15px;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 4px;
  cursor: pointer;
  transition: 0.1s;
}
.check:hover .box,
.uicheckbox:not(.row):not(.off):hover .box {
  border-color: var(--accent);
}
.box:after {
  content: '';
  position: absolute;
  left: 4px;
  top: 1px;
  width: 4px;
  height: 7px;
  border-right: 2px solid #fff;
  border-bottom: 2px solid #fff;
  transform: rotate(45deg);
  opacity: 0;
}
input:checked + .box {
  background: var(--accent);
  border-color: var(--accent);
}
input:checked + .box:after {
  opacity: 1;
}
input:focus-visible + .box {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
}
</style>
