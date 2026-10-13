<script setup lang="ts">
import { useConfirm } from '../../stores/confirm'
import UiButton from './UiButton.vue'
import UiDialog from './UiDialog.vue'

// Хост промис-подтверждения (замена legacy askModal): рендерит стор
// confirm через UiDialog. info-режим - без «Отмены», ярлык «Закрыть»
// уже подставлен стором. Монтируется один раз в NextLayout.
const { state, ok, cancel } = useConfirm()
</script>

<template>
  <UiDialog
    :open="state.open"
    :title="state.title"
    @update:open="(v) => !v && cancel()"
  >
    <div class="text">
      {{ state.text }}
    </div>
    <template #footer>
      <UiButton
        v-if="!state.info"
        @click="cancel()"
      >
        Отмена
      </UiButton>
      <UiButton
        variant="primary"
        @click="ok()"
      >
        {{ state.okLabel }}
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.text {
  font-size: 13px;
  white-space: pre-line;
  line-height: 1.5;
}
</style>
