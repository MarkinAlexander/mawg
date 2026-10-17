<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { dialogBackClose, dialogBackOpen } from './dialogBack'

// Модальный диалог на нативном <dialog> (паритет legacy: панельный фон,
// ::backdrop, busy гасит контент как dialog.busy > form). jsdom не всегда
// умеет showModal/close - там фолбэк на атрибут open.
const props = withDefaults(
  defineProps<{
    open: boolean
    title: string
    busy?: boolean
    width?: string
    // locked запрещает закрытие по Esc/клику на backdrop (легаси
    // dlgUpdate/dlgAuth: dialog 'cancel' -> preventDefault)
    locked?: boolean
  }>(),
  { busy: false, width: undefined, locked: false },
)

const emit = defineEmits<{ 'update:open': [value: boolean] }>()

const dlg = ref<HTMLDialogElement | null>(null)

function openNative() {
  const d = dlg.value
  if (!d) return
  if (typeof d.showModal === 'function') d.showModal()
  else d.setAttribute('open', '')
}
function closeNative() {
  const d = dlg.value
  if (!d || !d.open) return
  if (typeof d.close === 'function') d.close()
  else d.removeAttribute('open')
}

function onClose() {
  emit('update:open', false)
}

// единое закрытие «не из нативного close»: close() в jsdom-фолбэке не
// диспатчит событие close - эмитим напрямую (в браузере выйдет двойной
// одинаковый emit - потребители идемпотентны)
function requestClose() {
  closeNative()
  onClose()
}

// клик по пустому месту (слово владельца: закрывать «как полагается»):
// паддинг панели переехал в .dcontent, поэтому target === dialog только
// у backdrop; locked/busy не закрываем
function onClick(e: MouseEvent) {
  if (props.locked || props.busy) return
  if (e.target === dlg.value) requestClose()
}

// «Назад» браузера/Android закрывает незалоченную модалку (слово владельца)
let closedByPop = false
const backCloser = () => {
  closedByPop = true
  requestClose()
}

watch(
  () => props.open,
  async (v) => {
    await nextTick()
    if (v) {
      openNative()
      if (!props.locked) dialogBackOpen(backCloser)
    } else {
      closeNative()
      dialogBackClose(backCloser, closedByPop)
      closedByPop = false
    }
  },
  { immediate: true },
)

function onCancel(e: Event) {
  if (props.locked) e.preventDefault()
}

onBeforeUnmount(() => {
  if (props.open && !props.locked) dialogBackClose(backCloser, closedByPop)
  closeNative()
})
</script>

<template>
  <!-- showModal выводит dialog в top layer: teleport не нужен -->
  <dialog
    ref="dlg"
    class="uidialog"
    :style="props.width ? { maxWidth: props.width } : undefined"
    @close="onClose"
    @cancel="onCancel"
    @click="onClick"
  >
    <div
      class="dcontent"
      :class="{ busy: props.busy }"
    >
      <h3>{{ props.title }}</h3>
      <slot />
      <div
        v-if="$slots.footer"
        class="btnrow"
      >
        <slot name="footer" />
      </div>
    </div>
  </dialog>
</template>

<style scoped>
.uidialog {
  background: var(--panel);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 10px;
  /* паддинг в .dcontent: клики по рамке-полю панели не должны закрывать
     (target === dialog = только backdrop) */
  padding: 0;
  min-width: 360px;
  max-width: min(92vw, 480px);
  max-height: calc(100dvh - 32px);
  overflow-y: auto;
}
.uidialog::backdrop {
  background: rgba(0, 0, 0, 0.55);
}
h3 {
  margin: 0 0 6px;
}
.dcontent {
  padding: 18px;
  display: grid;
  gap: 10px;
}
.dcontent.busy {
  opacity: 0.55;
  pointer-events: none;
  transition: opacity 0.2s;
}
.btnrow {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  justify-content: end;
}
</style>
