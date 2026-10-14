<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import UiField from './UiField.vue'
import { COLOR_PRESETS, hexToHsv, hsvToHex, isValidHex, normalizeHex, type Hsv } from './color'

// Пикер цвета: кнопка-свотч + дропдаун (пресеты панели, SV-квадрат,
// полоса оттенка, hex-поле). Единый вид во всех браузерах - вместо
// нативного input[type=color], который в Firefox рисует системный виджет.
// bare - только кнопка (инлайн-ряды), иначе - через UiField с подписью.
const props = withDefaults(
  defineProps<{
    modelValue: string
    label?: string
    tooltip?: string
    // ширина кнопки-свотча; в labeled-режиме контрол занимает всю ширину
    bare?: boolean
  }>(),
  { label: undefined, tooltip: undefined, bare: false },
)

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const open = ref(false)
const root = ref<HTMLElement | null>(null)
const hexInput = ref('')
const hsv = ref<Hsv>({ h: 210, s: 1, v: 1 })

const norm = computed(() => normalizeHex(props.modelValue) ?? '#4a9eff')

// синхронизация пикера с внешним значением (и с пресетами)
watch([open, () => props.modelValue], ([o]) => {
  if (o) {
    hsv.value = hexToHsv(props.modelValue);
    hexInput.value = norm.value;
  }
}, { immediate: true });

function emitHex(hex: string): void {
  emit('update:modelValue', normalizeHex(hex) ?? props.modelValue);
}

function setHsv(next: Partial<Hsv>): void {
  hsv.value = { ...hsv.value, ...next };
  hexInput.value = hsvToHex(hsv.value);
  emitHex(hexInput.value);
}

function pickPreset(hex: string): void {
  emitHex(hex);
  open.value = false;
}

function onHexInput(e: Event): void {
  const v = (e.target as HTMLInputElement).value;
  hexInput.value = v;
  const n = normalizeHex(v);
  if (n) {
    hsv.value = hexToHsv(n);
    emitHex(n);
  }
}

// --- SV-квадрат и полоса оттенка: драг указателем ---
const svEl = ref<HTMLElement | null>(null);
const hueEl = ref<HTMLElement | null>(null);
let dragKind: 'sv' | 'hue' | null = null;

function applySv(e: PointerEvent): void {
  const el = svEl.value;
  if (!el) return;
  const r = el.getBoundingClientRect();
  const s = Math.min(1, Math.max(0, (e.clientX - r.left) / r.width));
  const v = 1 - Math.min(1, Math.max(0, (e.clientY - r.top) / r.height));
  setHsv({ s: Math.round(s * 100) / 100, v: Math.round(v * 100) / 100 });
}
function applyHue(e: PointerEvent): void {
  const el = hueEl.value;
  if (!el) return;
  const r = el.getBoundingClientRect();
  const h = Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)) * 360;
  setHsv({ h: Math.round(h) });
}
function onPointerDown(kind: 'sv' | 'hue', e: PointerEvent): void {
  dragKind = kind;
  try {
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
  } catch { /* jsdom */ }
  if (kind === 'sv') applySv(e);
  else applyHue(e);
}
function onPointerMove(e: PointerEvent): void {
  if (dragKind === 'sv') applySv(e);
  else if (dragKind === 'hue') applyHue(e);
}
function onPointerUp(): void {
  dragKind = null;
}

function onDocPointerDown(e: PointerEvent): void {
  if (open.value && root.value && !root.value.contains(e.target as Node)) open.value = false
}
function onDocKeydown(e: KeyboardEvent): void {
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

const svBg = computed(() => ({
  background: `linear-gradient(to top, #000, rgba(0,0,0,0)), linear-gradient(to right, #fff, hsl(${hsv.value.h}, 100%, 50%))`,
}));
const svDot = computed(() => ({ left: `${hsv.value.s * 100}%`, top: `${(1 - hsv.value.v) * 100}%`, background: norm.value }));
const hexInvalid = computed(() => hexInput.value !== '' && !isValidHex(hexInput.value));
</script>

<template>
  <UiField
    v-if="!props.bare"
    :label="props.label ?? ''"
    :tooltip="props.tooltip"
  >
    <div
      ref="root"
      class="colorroot full"
    >
      <button
        type="button"
        class="cbtn"
        :class="{ on: open }"
        :style="{ background: norm }"
        :title="props.tooltip || norm"
        @click="open = !open"
      />
      <span
        v-if="!open"
        class="chex muted"
      >{{ norm }}</span>
      <div
        v-if="open"
        class="cpop"
      >
        <div class="presets">
          <button
            v-for="p in COLOR_PRESETS"
            :key="p"
            type="button"
            class="swatch"
            :style="{ background: p }"
            :title="p"
            @click="pickPreset(p)"
          />
        </div>
        <div
          ref="svEl"
          class="sv"
          :style="svBg"
          @pointerdown="onPointerDown('sv', $event)"
          @pointermove="onPointerMove"
          @pointerup="onPointerUp"
        >
          <span
            class="dot"
            :style="svDot"
          />
        </div>
        <div
          ref="hueEl"
          class="hue"
          @pointerdown="onPointerDown('hue', $event)"
          @pointermove="onPointerMove"
          @pointerup="onPointerUp"
        >
          <span
            class="dot"
            :style="{ left: `${(hsv.h / 360) * 100}%`, background: hsvToHex({ h: hsv.h, s: 1, v: 1 }) }"
          />
        </div>
        <input
          class="hex"
          :class="{ bad: hexInvalid }"
          :value="hexInput"
          spellcheck="false"
          placeholder="#4a9eff"
          @input="onHexInput"
        >
      </div>
    </div>
  </UiField>
  <div
    v-else
    ref="root"
    class="colorroot"
  >
    <button
      type="button"
      class="cbtn"
      :class="{ on: open }"
      :style="{ background: norm }"
      :title="props.tooltip || norm"
      @click="open = !open"
    />
    <div
      v-if="open"
      class="cpop"
    >
      <div class="presets">
        <button
          v-for="p in COLOR_PRESETS"
          :key="p"
          type="button"
          class="swatch"
          :style="{ background: p }"
          :title="p"
          @click="pickPreset(p)"
        />
      </div>
      <div
        ref="svEl"
        class="sv"
        :style="svBg"
        @pointerdown="onPointerDown('sv', $event)"
        @pointermove="onPointerMove"
        @pointerup="onPointerUp"
      >
        <span
          class="dot"
          :style="svDot"
        />
      </div>
      <div
        ref="hueEl"
        class="hue"
        @pointerdown="onPointerDown('hue', $event)"
        @pointermove="onPointerMove"
        @pointerup="onPointerUp"
      >
        <span
          class="dot"
          :style="{ left: `${(hsv.h / 360) * 100}%`, background: hsvToHex({ h: hsv.h, s: 1, v: 1 }) }"
        />
      </div>
      <input
        class="hex"
        :class="{ bad: hexInvalid }"
        :value="hexInput"
        spellcheck="false"
        placeholder="#4a9eff"
        @input="onHexInput"
      >
    </div>
  </div>
</template>

<style scoped>
.colorroot {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
.colorroot.full {
  display: flex;
  width: 100%;
}
.cbtn {
  width: 42px;
  height: 31px;
  flex: none;
  padding: 0;
  border: 1px solid var(--border);
  border-radius: 6px;
  cursor: pointer;
}
.cbtn.on {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
}
.chex {
  font-size: 12px;
  font-family: ui-monospace, monospace;
}
.muted {
  color: var(--dim);
}
.cpop {
  position: absolute;
  top: calc(100% + 4px);
  left: 0;
  z-index: 70;
  width: 232px;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: 0 10px 28px rgba(0, 0, 0, 0.55);
  padding: 10px;
  display: grid;
  gap: 10px;
  cursor: default;
}
.presets {
  display: grid;
  grid-template-columns: repeat(8, 1fr);
  gap: 5px;
}
.swatch {
  width: 100%;
  height: 20px;
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 4px;
  cursor: pointer;
  padding: 0;
}
.swatch:hover {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
}
.sv {
  position: relative;
  height: 140px;
  border-radius: 6px;
  border: 1px solid var(--border);
  cursor: crosshair;
  touch-action: none;
}
.sv .dot,
.hue .dot {
  position: absolute;
  width: 12px;
  height: 12px;
  border-radius: 50%;
  border: 2px solid #fff;
  box-shadow: 0 0 0 1px rgba(0, 0, 0, 0.6);
  transform: translate(-50%, -50%);
  pointer-events: none;
}
.hue {
  position: relative;
  height: 14px;
  border-radius: 7px;
  border: 1px solid var(--border);
  background: linear-gradient(
    to right,
    #f00 0%,
    #ff0 17%,
    #0f0 33%,
    #0ff 50%,
    #00f 67%,
    #f0f 83%,
    #f00 100%
  );
  cursor: ew-resize;
  touch-action: none;
}
.hex {
  width: 100%;
  box-sizing: border-box;
  background: var(--bg);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 5px 8px;
  font-size: 13px;
  font-family: ui-monospace, monospace;
}
.hex.bad {
  border-color: var(--err);
}
</style>
