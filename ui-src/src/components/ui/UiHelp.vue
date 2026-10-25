<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

// UiHelp - кликабельный знак вопроса [?] рядом с термином/настройкой:
// поповер с подробной подсказкой. Клик - открыть/закрыть, клик мимо и
// Escape - закрыть (механика приватного режима, 62-док).
// Позиционирование - JS-ом position:fixed с клампом в границы экрана
// (67-док, слово владельца: absolute-поповер вылезает за край и даёт
// горизонтальный скролл; вниз не влезает - открываем вверх, совсем не
// влезает - прижимаем к краю, внутренняя прокрутка - последний рубеж).
// Скролл/ресайз закрывают поповер - он не должен «отвисать» от своего [?].
const props = defineProps<{ text: string }>()
const open = ref(false)
const root = ref<HTMLElement | null>(null)
const popStyle = ref<Record<string, string>>({ visibility: 'hidden' })

function close(): void {
  open.value = false
}

function onDocClick(e: MouseEvent): void {
  if (open.value && root.value && !root.value.contains(e.target as Node)) close()
}
function onKey(e: KeyboardEvent): void {
  if (e.key === 'Escape') close()
}
// capture: скролл любого контейнера (не только window) тоже закрывает
function onViewportChange(): void {
  if (open.value) close()
}

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

// позиционирование - в post-вотчере: DOM поповера гарантированно
// отрисован (после обычного nextTick из клика его ещё нет - гонка с
// планировщиком, кейс тестов UiHelp)
watch(
  open,
  (v) => {
    if (!v) return
    void (async () => {
      const btn = root.value?.querySelector<HTMLButtonElement>('.uihelp')
      const pop = root.value?.querySelector<HTMLElement>('.uihelp-pop')
      if (!btn || !pop) return
      const margin = 8
      // видимая область БЕЗ полосы прокрутки: innerWidth её включает, и
      // прижатый к innerWidth поповер вылезал на ширину скроллбара
      const vw = Math.min(window.innerWidth, document.documentElement.clientWidth || window.innerWidth)
      const vh = Math.min(window.innerHeight, document.documentElement.clientHeight || window.innerHeight)
      const width = Math.min(290, vw - margin * 2)
      // сначала ширина (невидимым): от неё зависит высота текста
      popStyle.value = {
        visibility: 'hidden',
        position: 'fixed',
        left: '0px',
        top: '0px',
        width: `${width}px`,
        maxHeight: `${vh - margin * 2}px`,
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
        left: `${Math.round(left)}px`,
        top: `${Math.round(top)}px`,
        width: `${width}px`,
        maxHeight: `${vh - margin * 2}px`,
      }
    })()
  },
  { flush: 'post' },
)
</script>

<template>
  <span
    ref="root"
    class="uihelp-wrap"
  >
    <button
      type="button"
      class="uihelp"
      :class="{ on: open }"
      :aria-expanded="open"
      title="подсказка"
      @click="open = !open"
    >
      ?
    </button>
    <span
      v-if="open"
      class="uihelp-pop"
      role="note"
      :style="popStyle"
    >{{ props.text }}</span>
  </span>
</template>

<style scoped>
.uihelp-wrap {
  position: relative;
  display: inline-flex;
  margin-left: 5px;
  vertical-align: baseline;
}
.uihelp {
  width: 15px;
  height: 15px;
  line-height: 12px;
  border-radius: 50%;
  border: 1px solid var(--border);
  background: transparent;
  color: var(--dim);
  font-size: 10px;
  font-weight: 600;
  cursor: help;
  padding: 0;
  flex: none;
}
.uihelp:hover,
.uihelp.on {
  color: var(--accent);
  border-color: var(--accent);
}
.uihelp-pop {
  z-index: 60;
  background: var(--panel2);
  border: 1px solid var(--accent);
  border-radius: 8px;
  padding: 8px 10px;
  font-size: 12px;
  line-height: 1.5;
  font-weight: normal;
  color: var(--text);
  white-space: pre-line;
  text-align: left;
  box-shadow: 0 6px 18px rgba(0, 0, 0, 0.35);
  overflow-y: auto;
  /* запрет переносов внутри слов: ширина даётся с запасом, длинные
     слова не бьются (слово владельца: «слова бьются - не профессионально») */
  overflow-wrap: normal;
  word-break: normal;
  hyphens: none;
}
</style>
