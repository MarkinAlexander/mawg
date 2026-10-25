<script setup lang="ts">
import { computed } from 'vue'
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { SmartCascadeSettings, SmartCascadeState } from '../../api/types'

// Чип умного каскада (86-док, слово владельца: «чтобы было понятно, что
// включено правило смарткаскада, не заходя в настройки; по клику - инфа»):
// бейдж на карточке пула / строке интерфейса; клик - поповер с планом
// (пути, триггер, возврат, антифлап) и живым состоянием. Позиционирование
// и закрытие - механика UiHelp (fixed + кламп, скролл/Escape закрывают).
const props = defineProps<{
  settings: SmartCascadeSettings
  state?: SmartCascadeState | null
}>()

const open = ref(false)
const root = ref<HTMLElement | null>(null)
const popStyle = ref<Record<string, string>>({ visibility: 'hidden' })

const on = computed(() => !!props.state?.on)
const chipText = computed(() =>
  on.value ? `через ${props.state?.via} (каскад)` : 'умный каскад')

const viaList = computed(() =>
  (props.settings.via || []).map((v, i) => `${i + 1}. ${v}`).join('\n') || '—')
const trigger = computed(() => props.settings.triggerMode === 'fails'
  ? `после ${(props.settings.triggerFails || 0) > 0 ? props.settings.triggerFails : 3} отказов подряд (тот же конфиг сразу пробуется через запасной)`
  : 'после перебора всех конфигов пула')
const ret = computed(() => {
  const r = props.settings.return || {}
  if (r.mode === 'interval') return `раз в ${(r.intervalH || 0) > 0 ? r.intervalH : 6} ч`
  if (r.mode === 'manual') return 'только кнопкой «Проверить прямой сейчас»'
  const times = r.timeOfDay && r.timeOfDay.length ? r.timeOfDay : ['03:00']
  return `по расписанию ${times.join(', ')}`
})
const dwell = computed(() => `${(props.settings.minDwellMin || 0) > 0 ? props.settings.minDwellMin : 10} мин`)

const fmtTime = (iso?: string): string => {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return ''
  const hm = d.toTimeString().slice(0, 5)
  return d.toDateString() === new Date().toDateString()
    ? hm
    : `${String(d.getDate()).padStart(2, '0')}.${String(d.getMonth() + 1).padStart(2, '0')} ${hm}`
}
const statusLines = computed(() => {
  const s = props.state
  if (!s?.on) return ['ждёт блокировки пула (правило не действует)']
  const lines: string[] = []
  lines.push(`трафик идёт через ${s.via}`)
  const since = fmtTime(s.since)
  if (since) lines.push(`включён: ${since}`)
  const next = fmtTime(s.nextCheck)
  if (next) lines.push(`следующая проверка прямого: ${next}`)
  if (s.lastDetail) lines.push(`детали: ${s.lastDetail}`)
  return lines
})

function close(): void { open.value = false }
function onDocClick(e: MouseEvent): void {
  if (open.value && root.value && !root.value.contains(e.target as Node)) close()
}
function onKey(e: KeyboardEvent): void { if (e.key === 'Escape') close() }
function onViewportChange(): void { if (open.value) close() }
onMounted(() => {
  document.addEventListener('click', onDocClick, true)
  document.addEventListener('keydown', onKey)
  window.addEventListener('scroll', onViewportChange, true)
  window.addEventListener('resize', onViewportChange)
})
onBeforeUnmount(() => {
  document.removeEventListener('click', onDocClick, true)
  document.removeEventListener('keydown', onKey)
  window.removeEventListener('scroll', onViewportChange, true)
  window.removeEventListener('resize', onViewportChange)
})
watch(
  open,
  (v) => {
    if (!v) return
    void (async () => {
      const btn = root.value?.querySelector<HTMLButtonElement>('.scchip')
      const pop = root.value?.querySelector<HTMLElement>('.scpop')
      if (!btn || !pop) return
      const margin = 8
      const vw = window.innerWidth
      const vh = window.innerHeight
      const width = Math.min(320, vw - margin * 2)
      popStyle.value = {
        visibility: 'hidden', position: 'fixed', left: '0px', top: '0px',
        width: `${width}px`, maxHeight: `${vh - margin * 2}px`,
      }
      await nextTick()
      const b = btn.getBoundingClientRect()
      const ph = pop.offsetHeight
      let left = b.left
      if (left + width > vw - margin) left = vw - margin - width
      if (left < margin) left = margin
      let top = b.bottom + 6
      if (top + ph > vh - margin) {
        const above = b.top - 6 - ph
        top = above >= margin ? above : Math.max(margin, vh - margin - ph)
      }
      popStyle.value = {
        position: 'fixed',
        left: `${Math.round(left)}px`, top: `${Math.round(top)}px`,
        width: `${width}px`, maxHeight: `${vh - margin * 2}px`,
      }
    })()
  },
  { flush: 'post' },
)
</script>

<template>
  <span
    ref="root"
    class="scchip-wrap"
  >
    <button
      type="button"
      class="scchip"
      :class="on ? 'v-on' : 'v-idle'"
      :aria-expanded="open"
      title="умный каскад - нажмите для подробностей"
      @click="open = !open"
    >
      {{ chipText }}
    </button>
    <div
      v-if="open"
      class="scpop"
      role="note"
      :style="popStyle"
    >
      <div class="ttl">
        Умный каскад
      </div>
      <div class="row">
        <b>Запасные пути</b><span class="pre">{{ viaList }}</span>
      </div>
      <div class="row">
        <b>Включение</b><span>{{ trigger }}</span>
      </div>
      <div class="row">
        <b>Возврат</b><span>{{ ret }}</span>
      </div>
      <div class="row">
        <b>Антифлап</b><span>{{ dwell }}</span>
      </div>
      <div class="row">
        <b>Сейчас</b><span class="pre">{{ statusLines.join('\n') }}</span>
      </div>
      <div
        v-if="state?.ensureErr"
        class="row err"
      >
        <b>Проблема</b><span>{{ state.ensureErr }}</span>
      </div>
    </div>
  </span>
</template>

<style scoped>
.scchip-wrap {
  position: relative;
  display: inline-flex;
  vertical-align: baseline;
}
/* фирменный цвет умного каскада (слово владельца: #27d1ce): спокойный
   чип - контурный полупрозрачный, активный - плотная бирюза с мягким
   свечением; оба состояния в одной гамме, hover подсвечивает */
.scchip {
  --sc: #27d1ce;
  --sc-text: #2fd9d6;
  font-size: 11px;
  line-height: 1;
  border-radius: 10px;
  padding: 4px 8px;
  border: 1px solid var(--border);
  background: var(--bg);
  cursor: pointer;
}
.scchip.v-idle {
  color: var(--sc-text);
  border-color: rgba(39, 209, 206, 0.45);
  background: rgba(39, 209, 206, 0.07);
}
.scchip.v-idle:hover {
  background: rgba(39, 209, 206, 0.14);
}
.scchip.v-on {
  color: var(--sc-text);
  border-color: var(--sc);
  background: rgba(39, 209, 206, 0.16);
  box-shadow: 0 0 0 1px rgba(39, 209, 206, 0.25);
}
.scchip.v-on:hover {
  background: rgba(39, 209, 206, 0.24);
}
.scpop {
  z-index: 60;
  background: var(--panel2);
  border: 1px solid var(--accent);
  border-radius: 8px;
  padding: 8px 10px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--text);
  text-align: left;
  box-shadow: 0 6px 18px rgba(0, 0, 0, 0.35);
  overflow-y: auto;
  display: grid;
  gap: 4px;
  overflow-wrap: normal;
  word-break: normal;
  hyphens: none;
}
.scpop .ttl {
  font-weight: 600;
  margin-bottom: 2px;
}
.scpop .row {
  display: flex;
  gap: 8px;
  align-items: baseline;
}
.scpop .row b {
  flex: none;
  width: 96px;
  color: var(--dim);
  font-weight: 600;
}
.scpop .row span {
  min-width: 0;
}
.scpop .pre {
  white-space: pre-line;
}
.scpop .err span {
  color: var(--err, #c66);
}
</style>
