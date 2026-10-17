<script setup lang="ts">
import { ref, watch } from 'vue'
import { UiButton, UiDialog, UiHelp, UiHint, UiInput } from '../../components/ui'
import { useCaptcha } from '../../stores/captcha'
import { G } from '../glossary'

// Хост капчи Amnezia (легаси dlgCaptcha): монтируется рядом с ConfirmHost.
const { state, refresh, answer, cancel } = useCaptcha();
const solution = ref('');
const refreshing = ref(false);

watch(
  () => state.open,
  (v) => {
    if (v) solution.value = '';
  },
);

function send(): void {
  const v = solution.value.trim();
  if (!v) return;
  answer(v);
}
</script>

<template>
  <UiDialog
    v-model:open="state.open"
    title="Проверка Amnezia Free"
    width="420px"
  >
    <div class="capbox">
      <img
        :src="'data:image/png;base64,' + state.image"
        alt="капча"
        class="capimg"
      >
      <UiButton
        variant="icon"
        tooltip="обновить картинку"
        :busy="refreshing"
        @click="refreshing = true; void refresh().finally(() => refreshing = false)"
      >
        ↻
      </UiButton>
    </div>
    <UiHelp :text="G.capcha" />
    <UiHint :text="state.hint" />
    <UiInput
      v-model="solution"
      label="Решение"
      placeholder="_ _ _ _ _"
      @keyup.enter="send"
    />
    <template #footer>
      <UiButton @click="cancel">
        Отмена
      </UiButton>
      <UiButton
        variant="primary"
        @click="send"
      >
        Отправить
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.capbox {
  display: flex;
  align-items: center;
  gap: 8px;
}
.capimg {
  max-width: 100%;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: #fff;
  min-height: 60px;
}
</style>
