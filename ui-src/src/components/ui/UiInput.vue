<script setup lang="ts">
import { computed, ref } from 'vue'
import UiField from './UiField.vue'
import UiIcon from './UiIcon.vue'

// Инпут на UiField: label/hint/tooltip/error, валидатор на ввод,
// mono-вариант, пароль с глазом (legacy .pwrap/.peye).
const props = withDefaults(
  defineProps<{
    modelValue: string
    label?: string
    hint?: string
    tooltip?: string
    placeholder?: string
    type?: 'text' | 'password' | 'number'
    // шаг степпера для number (стрелки в стиле панели)
    step?: number
    mono?: boolean
    disabled?: boolean
    required?: boolean
    validator?: (value: string) => string | null
    // без UiField - «голый» контрол для инлайн-рядов (как bare у UiSwitch)
    bare?: boolean
    // нативный autocomplete инпута (логин-форма: username/current-password)
    autocomplete?: string
  }>(),
  {
    label: undefined,
    type: 'text',
    step: 1,
    hint: undefined,
    tooltip: undefined,
    placeholder: undefined,
    validator: undefined,
    bare: false,
    autocomplete: undefined,
  },
)

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const showPassword = ref(false)
const error = ref<string | null>(null)
const inputEl = ref<HTMLInputElement | null>(null)

// фокус из диалогов/валидаций (логин-диалог: автофокус пароля при открытии)
defineExpose({
  focus: () => inputEl.value?.focus(),
})

// крестик очистки (легаси wireClearInputs): только для текстовых полей
// с непустым значением; у password - глаз, у number - штатный спиннер
const canClear = computed(() => props.type === 'text' && !!props.modelValue && !props.disabled)

// степпер number: свои стрелки в стиле панели (нативный спиннер спрятан)
function stepValue(dir: number): void {
  const cur = Number.parseFloat(props.modelValue);
  const next = (Number.isFinite(cur) ? cur : 0) + dir * props.step;
  const v = String(next);
  emit('update:modelValue', v);
  error.value = props.validator ? props.validator(v) : null;
  inputEl.value?.focus();
}

function clear(): void {
  emit('update:modelValue', '')
  error.value = props.validator ? props.validator('') : null
  inputEl.value?.focus()
}

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
  <!-- bare: контрол без подписи/хинтов - для тулбар-рядов в одну линию -->
  <div
    v-if="props.bare"
    class="inline bare"
  >
    <span class="pwrap">
      <input
        ref="inputEl"
        :type="effType"
        :value="props.modelValue"
        :placeholder="props.placeholder"
        :disabled="props.disabled"
        :autocomplete="props.autocomplete"
        :class="{ mono: props.mono }"
        @input="onInput"
      >
      <span
        v-if="props.type === 'number' && !props.disabled"
        class="pnum"
      >
        <button
          type="button"
          class="pnum-btn"
          title="больше"
          @click="stepValue(1)"
        >
          <UiIcon name="chevron-up" />
        </button>
        <button
          type="button"
          class="pnum-btn"
          title="меньше"
          @click="stepValue(-1)"
        >
          <UiIcon name="chevron-down" />
        </button>
      </span>
      <button
        v-if="canClear"
        class="pclr"
        type="button"
        title="очистить"
        @click="clear"
      >
        <UiIcon name="x" />
      </button>
    </span>
    <div
      v-if="$slots.action"
      class="act"
    >
      <slot name="action" />
    </div>
  </div>
  <UiField
    v-else
    :label="props.label ?? ''"
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
            ref="inputEl"
            :type="effType"
            :value="props.modelValue"
            :placeholder="props.placeholder"
            :disabled="props.disabled"
            :autocomplete="props.autocomplete"
            :class="{ mono: props.mono }"
            @input="onInput"
          >
          <span
            v-if="props.type === 'number' && !props.disabled"
            class="pnum"
          >
            <button
              type="button"
              class="pnum-btn"
              title="больше"
              @click="stepValue(1)"
            >
              <UiIcon name="chevron-up" />
            </button>
            <button
              type="button"
              class="pnum-btn"
              title="меньше"
              @click="stepValue(-1)"
            >
              <UiIcon name="chevron-down" />
            </button>
          </span>
          <button
            v-if="canClear"
            class="pclr"
            type="button"
            title="очистить"
            @click="clear"
          >
            <UiIcon name="x" />
          </button>
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
  color: var(--ctl);
  line-height: 0;
  cursor: pointer;
}
.peye:hover {
  color: var(--ctl-hover);
  border-color: transparent;
}
/* number: нативный спиннер спрятан, свои стрелки в стиле панели
   (серые, при наведении зелёные - слово владельца) */
input[type='number']::-webkit-inner-spin-button,
input[type='number']::-webkit-outer-spin-button {
  -webkit-appearance: none;
  margin: 0;
}
input[type='number'] {
  -moz-appearance: textfield;
  appearance: textfield;
}
.pnum {
  position: absolute;
  right: 6px;
  top: 50%;
  transform: translateY(-50%);
  display: flex;
  flex-direction: column;
  gap: 1px;
}
.pnum-btn {
  background: none;
  border: none;
  padding: 0 2px;
  color: var(--ctl);
  line-height: 0;
  cursor: pointer;
}
.pnum-btn:hover {
  color: var(--ctl-hover);
}
.pclr {
  position: absolute;
  right: 4px;
  top: 50%;
  transform: translateY(-50%);
  background: none;
  border: none;
  padding: 2px;
  color: var(--ctl);
  line-height: 0;
  cursor: pointer;
}
.pclr:hover {
  color: var(--ctl-hover);
  border-color: transparent;
}
</style>
