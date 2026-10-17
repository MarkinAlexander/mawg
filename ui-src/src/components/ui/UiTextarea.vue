<script setup lang="ts">
import { ref } from 'vue'
import UiField from './UiField.vue'

// Textarea на UiField: label/hint/tooltip/error, валидатор на ввод, mono.
const props = withDefaults(
  defineProps<{
    modelValue: string
    label: string
    hint?: string
    tooltip?: string
    placeholder?: string
    rows?: number
    mono?: boolean
    disabled?: boolean
    required?: boolean
    validator?: (value: string) => string | null
  }>(),
  {
    rows: 4,
    hint: undefined,
    tooltip: undefined,
    placeholder: undefined,
    validator: undefined,
  },
)

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const error = ref<string | null>(null)

function onInput(e: Event) {
  const v = (e.target as HTMLTextAreaElement).value
  emit('update:modelValue', v)
  error.value = props.validator ? props.validator(v) : null
}
</script>

<template>
  <UiField
    :label="props.label"
    :hint="props.hint"
    :tooltip="props.tooltip"
    :required="props.required"
    :error="error ?? undefined"
  >
    <template #default="{ id }">
      <textarea
        :id="id"
        :value="props.modelValue"
        :rows="props.rows"
        :placeholder="props.placeholder"
        :disabled="props.disabled"
        :class="{ mono: props.mono }"
        @input="onInput"
      />
    </template>
  </UiField>
</template>

<style scoped>
textarea {
  display: block;
  width: 100%;
  margin-top: 3px;
  background: var(--bg);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 5px 8px;
  font-size: 13px;
  resize: vertical;
}
textarea.mono {
  font: 12px/1.5 ui-monospace, monospace;
}
</style>
