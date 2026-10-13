<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'

// Модальный диалог на нативном <dialog> (паритет legacy: панельный фон,
// ::backdrop, busy гасит контент как dialog.busy > form). jsdom не всегда
// умеет showModal/close - там фолбэк на атрибут open.
const props = withDefaults(
  defineProps<{
    open: boolean
    title: string
    busy?: boolean
    width?: string
  }>(),
  { busy: false, width: undefined },
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

watch(
  () => props.open,
  async (v) => {
    await nextTick()
    if (v) openNative()
    else closeNative()
  },
  { immediate: true },
)

function onClose() {
  emit('update:open', false)
}

onBeforeUnmount(closeNative)
</script>

<template>
  <!-- showModal выводит dialog в top layer: teleport не нужен -->
  <dialog
    ref="dlg"
    class="uidialog"
    :style="props.width ? { maxWidth: props.width } : undefined"
    @close="onClose"
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
  padding: 18px;
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
