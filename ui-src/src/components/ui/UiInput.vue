<script setup lang="ts">
import { computed, ref } from 'vue'
import UiField from './UiField.vue'
import UiIcon from './UiIcon.vue'

// Инпут на UiField: label/hint/tooltip/error, валидатор на ввод,
// mono-вариант, пароль с глазом (legacy .pwrap/.peye).
const props = withDefaults(
  defineProps<{
    modelValue: string
    label: string
    hint?: string
    tooltip?: string
    placeholder?: string
    type?: 'text' | 'password' | 'number'
    mono?: boolean
    disabled?: boolean
    required?: boolean
    validator?: (value: string) => string | null
  }>(),
  {
    type: 'text',
    hint: undefined,
    tooltip: undefined,
    placeholder: undefined,
    validator: undefined,
  },
)

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const showPassword = ref(false)
const error = ref<string | null>(null)

const effType = computed(() => {
  if (props.type !== 'password') return props.type
  return showPassword.value ? 'text' : 'password'
})

function onInput(e: Event) {
  const v = (e.target as HTMLInputElement).value
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
      <div class="inline">
        <span class="pwrap">
          <input
            :id="id"
            :type="effType"
            :value="props.modelValue"
            :placeholder="props.placeholder"
            :disabled="props.disabled"
            :class="{ mono: props.mono }"
            @input="onInput"
          >
          <button
            v-if="props.type === 'password'"
            class="peye"
            type="button"
            :title="showPassword ? 'скрыть пароль' : 'показать пароль'"
            @click="showPassword = !showPassword"
          >
            <UiIcon :name="showPassword ? 'eye-off' : 'eye'" />
          </button>
        </span>
        <!-- парная кнопка: одной высоты с инпутом, на его линии -->
        <div
          v-if="$slots.action"
          class="act"
        >
          <slot name="action" />
        </div>
      </div>
    </template>
  </UiField>
</template>

<style scoped>
.inline {
  display: flex;
  align-items: stretch;
  gap: 8px;
  margin-top: 3px;
}
.inline .pwrap {
  flex: 1;
  min-width: 0;
  margin-top: 0;
}
.act {
  flex: none;
  display: flex;
}
/* кнопка пары - высотой с инпут (31px = 5+19+5+2), на его базовой линии */
.act :deep(.uibtn) {
  height: auto;
  align-self: stretch;
  padding-top: 0;
  padding-bottom: 0;
}
.pwrap {
  position: relative;
  display: block;
  margin-top: 3px;
}
input {
  display: block;
  width: 100%;
  background: var(--bg);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 5px 8px;
  font-size: 13px;
}
.pwrap input {
  padding-right: 32px;
}
input.mono {
  font-family: ui-monospace, monospace;
}
.peye {
  position: absolute;
  right: 4px;
  top: 50%;
  transform: translateY(-50%);
  background: none;
  border: none;
  padding: 2px;
  color: var(--dim);
  line-height: 0;
  cursor: pointer;
}
.peye:hover {
  color: var(--text);
  border-color: transparent;
}
</style>
