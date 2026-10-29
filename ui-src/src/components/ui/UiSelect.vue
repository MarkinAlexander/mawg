<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
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
  label?: string
  hint?: string
  tooltip?: string
  disabled?: boolean
  busy?: boolean
  // без UiField - «голый» дропдаун для инлайн-рядов (как bare у UiSwitch)
  bare?: boolean
}>()

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const open = ref(false)
const root = ref<HTMLElement | null>(null)

const selected = computed(() => props.options.find((o) => o.value === props.modelValue))

// Список рисуется в координатах ЭКРАНА (position:fixed, размеры по месту
// под кнопкой): абсолютный дропдаун внутри модалки вылезал за её нижний
// край и заставлял её скроллиться, даже когда на экране место было
// (слово владельца 2026-10-29). Границы высоты 200-520px: не мельчаем и
// не съедаем экран; если снизу тесно, а сверху много - раскрываемся вверх.
function syncListBox() {
  const btn = root.value?.querySelector('.psel-btn') as HTMLElement | null
  const list = root.value?.querySelector('.psel-list') as HTMLElement | null
  if (!btn || !list) return
  const r = btn.getBoundingClientRect()
  if (r.top === 0 && r.bottom === 0) return // замера нет (jsdom) - css-дефолт
  const below = window.innerHeight - r.bottom - 8
  const above = r.top - 8
  const up = below < 220 && above > below
  list.style.position = 'fixed'
  list.style.left = `${Math.round(r.left)}px`
  list.style.top = up ? 'auto' : `${Math.round(r.bottom + 2)}px`
  list.style.bottom = up ? `${Math.round(window.innerHeight - r.top + 2)}px` : 'auto'
  list.style.maxHeight = `${Math.round(Math.max(200, Math.min(up ? above : below, 520)))}px`
  if (r.width > 0) list.style.minWidth = `${Math.round(r.width)}px`
  // не вылезать за правый край (кнопка у правой границы): сдвиг влево
  const lr = list.getBoundingClientRect()
  const overRight = lr.right - (window.innerWidth - 8)
  if (overRight > 0) list.style.left = `${Math.max(8, r.left - overRight)}px`
}

async function toggle() {
  open.value = !open.value
  if (open.value) {
    await nextTick()
    syncListBox()
  }
}

// список в экранных координатах: при скролле/ресайзе якорь уезжает -
// закрываем (стандарт дропдаунов)
function onViewportChange() {
  if (open.value) open.value = false
}

function onDocPointerDown(e: PointerEvent) {
  if (open.value && root.value && !root.value.contains(e.target as Node)) open.value = false
}
function onDocKeydown(e: KeyboardEvent) {
  if (open.value && e.key === 'Escape') open.value = false
}
onMounted(() => {
  document.addEventListener('pointerdown', onDocPointerDown)
  document.addEventListener('keydown', onDocKeydown)
  window.addEventListener('scroll', onViewportChange, true)
  window.addEventListener('resize', onViewportChange)
})
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDocPointerDown)
  document.removeEventListener('keydown', onDocKeydown)
  window.removeEventListener('scroll', onViewportChange, true)
  window.removeEventListener('resize', onViewportChange)
})

function pick(o: UiSelectOption) {
  open.value = false
  emit('update:modelValue', o.value)
}
</script>

<template>
  <!-- bare: дропдаун без подписи/хинтов - для тулбар-рядов в одну линию -->
  <div
    v-if="props.bare"
    class="inline bare"
  >
    <div
      ref="root"
      class="psel"
    >
      <button
        type="button"
        class="psel-btn"
        :class="{ on: open }"
        :disabled="props.disabled || props.busy"
        :title="selected?.label ?? props.tooltip"
        @click="toggle"
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
            :class="{ on: open }"
            :disabled="props.disabled || props.busy"
            :title="selected?.label ?? props.tooltip"
            @click="toggle"
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
  position: relative;
  width: 100%;
  text-align: left;
  font-size: 13px;
  padding: 5px 26px 5px 8px; /* справа место под стрелку-индикатор */
  background: var(--bg);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 6px;
  cursor: pointer;
}
/* стрелка-индикатор списка; при открытии разворачивается, при наведении
   зеленеет (единый стиль контролов через --ctl/--ctl-hover) */
.psel-btn::after {
  content: '';
  position: absolute;
  right: 10px;
  top: 50%;
  margin-top: -3px;
  border: 4px solid transparent;
  border-top-color: var(--ctl);
  transition: transform 0.15s;
}
.psel-btn:hover::after {
  border-top-color: var(--ctl-hover);
}
.psel-btn.on::after {
  transform: rotate(180deg);
}
.psel-btn:disabled {
  opacity: 0.55;
  cursor: default;
}
/* Дефолт (absolute под кнопкой) - пока JS не измерил геометрию; при
   открытии syncListBox переводит список в position:fixed по координатам
   экрана, чтобы не скроллить родителей (модалку/карточки) */
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
  /* на телефоне max-content (до 460) шире экрана и список уезжает за
     край - клемпим по вьюпорту, длинные опции переносятся на строки */
  max-width: min(460px, calc(100vw - 24px));
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
