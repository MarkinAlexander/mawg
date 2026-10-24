<script setup lang="ts">
import { computed, ref } from 'vue'
import UiBadge from './UiBadge.vue'
import UiButton from './UiButton.vue'
import UiHint from './UiHint.vue'
import UiSelect, { type UiSelectOption } from './UiSelect.vue'

// Упорядоченный список с добавлением из селекта - паттерн «Участники
// набора» (BundleDialog), вынесен в кит для переиспользования (умный
// каскад 86-док: Via-пулы по приоритету). Порядок строк = приоритет:
// стрелки вверх/вниз переставляют, «убрать» удаляет; выбранные
// скрываются из селекта добавления.
const props = defineProps<{
  modelValue: string[]
  // все кандидаты; label строки берётся отсюда по value
  options: UiSelectOption[]
  // подпись поля добавления (по умолчанию «добавить»)
  label?: string
  addLabel?: string
  emptyHint?: string
  orderHint?: string
  disabled?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()

const picked = ref('')
const addOptions = computed<UiSelectOption[]>(() => {
  const used = new Set(props.modelValue)
  const free = props.options.filter((o) => o.value !== '' && !used.has(o.value))
  return free.length ? free : [{ value: '', label: 'нет доступных' }]
})
const labelOf = (v: string) => props.options.find((o) => o.value === v)?.label || v

function add(): void {
  if (!picked.value) return
  emit('update:modelValue', [...props.modelValue, picked.value])
  picked.value = ''
}
function move(i: number, delta: number): void {
  const j = i + delta
  if (j < 0 || j >= props.modelValue.length) return
  const out = [...props.modelValue]
  ;[out[i], out[j]] = [out[j], out[i]]
  emit('update:modelValue', out)
}
function removeAt(i: number): void {
  const out = [...props.modelValue]
  out.splice(i, 1)
  emit('update:modelValue', out)
}
</script>

<template>
  <div class="uorderlist">
    <div
      v-for="(v, i) in modelValue"
      :key="v"
      class="row"
    >
      <UiBadge :text="labelOf(v)" />
      <span class="sp" />
      <UiButton
        variant="icon"
        tooltip="выше"
        :disabled="i === 0 || props.disabled"
        @click="move(i, -1)"
      >
        ↑
      </UiButton>
      <UiButton
        variant="icon"
        tooltip="ниже"
        :disabled="i === modelValue.length - 1 || props.disabled"
        @click="move(i, 1)"
      >
        ↓
      </UiButton>
      <UiButton
        variant="danger"
        :disabled="props.disabled"
        @click="removeAt(i)"
      >
        убрать
      </UiButton>
    </div>
    <UiHint
      v-if="!modelValue.length && props.emptyHint"
      :text="props.emptyHint"
    />
    <UiSelect
      v-model="picked"
      :options="addOptions"
      :label="props.label ?? 'добавить'"
      :disabled="props.disabled"
    >
      <template #action>
        <UiButton
          :disabled="!picked || props.disabled"
          @click="add"
        >
          {{ props.addLabel ?? 'добавить' }}
        </UiButton>
      </template>
    </UiSelect>
    <UiHint
      v-if="props.orderHint"
      :text="props.orderHint"
    />
  </div>
</template>

<style scoped>
.uorderlist {
  display: grid;
  gap: 2px;
  margin: 4px 0;
}
.uorderlist .row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 3px 0;
}
.uorderlist .sp {
  flex: 1;
}
</style>
