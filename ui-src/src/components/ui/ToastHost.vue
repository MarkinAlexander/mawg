<script setup lang="ts">
import { useToast } from '../../stores/toast'

// Хост тостов - рендер очереди стора toast (Фаза 1): фиксированный угол,
// рамка ok/err (в legacy warning-и ходят как err - стор паритетен), клик
// скрывает. Монтируется один раз в NextLayout.
const { toasts, dismiss } = useToast()
</script>

<template>
  <div class="toasts">
    <div
      v-for="t in toasts.list"
      :key="t.id"
      class="toast"
      :class="{ err: t.err }"
      title="нажмите, чтобы скрыть"
      @click="dismiss(t.id)"
    >
      {{ t.text }}
    </div>
  </div>
</template>

<style scoped>
.toasts {
  position: fixed;
  bottom: 16px;
  right: 16px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  z-index: 90;
  max-width: min(92vw, 420px);
}
.toast {
  background: var(--panel2);
  border: 1px solid var(--ok);
  border-radius: 8px;
  padding: 10px 14px;
  font-size: 13px;
  cursor: pointer;
  word-break: break-word;
}
.toast.err {
  border-color: var(--err);
}
</style>
