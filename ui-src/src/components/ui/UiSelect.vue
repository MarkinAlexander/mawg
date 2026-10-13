<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import UiField from './UiField.vue'

// Кастомный дропдаун в стиле legacy .psel: нативный select не умеет
// подсказки у опций и режет длинные пункты в Chromium. Закрытие - клик
// вне / Escape; у выбранного и у опций title с полным текстом.
export interface UiSelectOption {
  value: string
  label: string
  hint?: string
  count?: number
}

const props = defineProps<{
  modelValue: string
  options: UiSelectOption[]
  label: string
  hint?: string
  tooltip?: string
  disabled?: boolean
  busy?: boolean
}>()

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const open = ref(false)
const root = ref<HTMLElement | null>(null)

const selected = computed(() => props.options.find((o) => o.value === props.modelValue))

function onDocPointerDown(e: PointerEvent) {
  if (open.value && root.value && !root.value.contains(e.target as Node)) open.value = false
}
function onDocKeydown(e: KeyboardEvent) {
  if (open.value && e.key === 'Escape') open.value = false
}
onMounted(() => {
  document.addEventListener('pointerdown', onDocPointerDown)
  document.addEventListener('keydown', onDocKeydown)
})
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDocPointerDown)
  document.removeEventListener('keydown', onDocKeydown)
})

function pick(o: UiSelectOption) {
  open.value = false
  emit('update:modelValue', o.value)
}
</script>

<template>
  <UiField
    :label="props.label"
    :hint="props.hint"
    :tooltip="props.tooltip"
  >
    <template #default>
      <div class="inline">
        <div
          ref="root"
          class="psel"
        >
          <button
            type="button"
            class="psel-btn"
            :disabled="props.disabled || props.busy"
            :title="selected?.label ?? props.tooltip"
            @click="open = !open"
          >
            {{ selected?.label ?? '—' }}
          </button>
          <div
            v-if="open"
            class="psel-list"
          >
            <button
              v-for="o in props.options"
              :key="o.value"
              type="button"
              class="psel-item"
              :title="o.hint ? `${o.label}: ${o.hint}` : o.label"
              @click="pick(o)"
            >
              <span class="row">
                <span class="lbl">{{ o.label }}</span>
                <span
                  v-if="o.count !== undefined"
                  class="cnt"
                >{{ o.count }}</span>
              </span>
              <span
                v-if="o.hint"
                class="hint"
              >{{ o.hint }}</span>
            </button>
          </div>
        </div>
        <!-- парная кнопка: одной высоты с селектом, на его линии -->
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
.inline .psel {
  flex: 1;
  min-width: 0;
  margin-top: 0;
}
.act {
  flex: none;
  display: flex;
}
/* кнопка пары - высотой с селект, на его базовой линии */
.act :deep(.uibtn) {
  height: auto;
  align-self: stretch;
  padding-top: 0;
  padding-bottom: 0;
}
.psel {
  position: relative;
  margin-top: 3px;
}
.psel-btn {
  width: 100%;
  text-align: left;
  font-size: 13px;
  padding: 5px 8px;
  background: var(--bg);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 6px;
  cursor: pointer;
}
.psel-btn:disabled {
  opacity: 0.55;
  cursor: default;
}
.psel-list {
  position: absolute;
  top: calc(100% + 2px);
  left: 0;
  z-index: 60;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 6px;
  max-height: 340px;
  overflow: auto;
  box-shadow: 0 10px 28px rgba(0, 0, 0, 0.55);
  width: max-content;
  min-width: 100%;
  max-width: 460px;
}
.psel-item {
  display: flex;
  flex-direction: column;
  gap: 1px;
  width: 100%;
  text-align: left;
  background: none;
  border: 0;
  border-bottom: 1px solid var(--border);
  border-radius: 0;
  color: var(--text);
  padding: 7px 10px;
  font-size: 13px;
  cursor: pointer;
}
.psel-item:last-child {
  border-bottom: 0;
}
.psel-item:hover {
  background: var(--panel2);
}
.psel-item .row {
  display: flex;
  gap: 8px;
  align-items: baseline;
}
.psel-item .lbl {
  white-space: normal;
  word-break: break-word;
}
.psel-item .cnt {
  margin-left: auto;
  flex: none;
  color: var(--dim);
  font-size: 11px;
}
.psel-item .hint {
  color: var(--dim);
  font-size: 11px;
  white-space: normal;
  word-break: break-word;
}
</style>
