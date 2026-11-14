<script setup lang="ts">
// Оверлей «идёт применение»: затемняет блок, крутит спиннер, пишет что
// происходит - явный фидбек на медленные операции (пересборка движка при
// смене режима, проверка соединения, загрузка конфигов), вместо «висит
// и непонятно, живо ли» (слово владельца 14.11, 118). Родителю - position:
// relative, оверлей ложится absolute поверх всего содержимого.
withDefaults(
  defineProps<{
    show: boolean
    text?: string
  }>(),
  { text: 'идёт применение настроек…' },
);
</script>

<template>
  <Transition name="uiboo">
    <div
      v-if="show"
      class="uiboo"
      role="status"
    >
      <span class="spin" />
      <span class="txt">{{ text }}</span>
    </div>
  </Transition>
</template>

<style scoped>
.uiboo {
  position: absolute;
  inset: 0;
  z-index: 5;
  display: flex;
  flex-direction: column;
  gap: 10px;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.45);
  border-radius: inherit;
}
.spin {
  width: 22px;
  height: 22px;
  border: 3px solid var(--accent);
  border-right-color: transparent;
  border-radius: 50%;
  animation: uiboo-spin 0.7s linear infinite;
}
.txt {
  font-size: 13px;
  color: var(--text);
  user-select: none;
}
.uiboo-enter-active,
.uiboo-leave-active {
  transition: opacity 0.2s;
}
.uiboo-enter-from,
.uiboo-leave-to {
  opacity: 0;
}
@keyframes uiboo-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
