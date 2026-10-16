<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

// UiHelp - кликабельный знак вопроса [?] рядом с термином/настройкой:
// поповер с подробной подсказкой. Клик - открыть/закрыть, клик мимо и
// Escape - закрыть (та же механика, что у приватного режима, 62-док).
// Текст поддерживает переводы строк (pre-line). Самодокументация панели:
// подробности живут в интерфейсе, а не в журналах сессий.
const props = defineProps<{ text: string }>()
const open = ref(false)
const root = ref<HTMLElement | null>(null)

function onDocClick(e: MouseEvent): void {
  if (open.value && root.value && !root.value.contains(e.target as Node)) open.value = false
}
function onKey(e: KeyboardEvent): void {
  if (e.key === 'Escape') open.value = false
}
onMounted(() => {
  document.addEventListener('click', onDocClick, true)
  document.addEventListener('keydown', onKey)
})
onBeforeUnmount(() => {
  document.removeEventListener('click', onDocClick, true)
  document.removeEventListener('keydown', onKey)
})
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
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  z-index: 40;
  width: 290px;
  max-width: 78vw;
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
}
</style>
