<script setup lang="ts">
import { UiBanner, UiButton, UiHelp, UiInput } from '../../components/ui';
import { splitIps } from './poolSettingsLogic';

// секция «Исключения пула» (94-док): чипы адресов с ✕ + пачка через
// разделители. Список - model: предзаполнение и dirty-гейт живут в
// диалоге, сюда приезжает готовый черновик
const list = defineModel<string[]>('list', { default: () => [] });
const draft = defineModel<string>('draft', { default: '' });
defineProps<{ disabled: boolean }>();

function exclAdd(): void {
  const parts = splitIps(draft.value);
  for (const x of parts) {
    if (!list.value.includes(x)) list.value.push(x);
  }
  draft.value = '';
}
function exclRemove(i: number): void {
  list.value.splice(i, 1);
}
</script>

<template>
  <div class="sect">
    Исключения пула<UiHelp text="Эти адреса пул не обрабатывает - трафик на них и с них идёт напрямую, мимо туннеля (свои серверы за этим пулом и т.п.). Общий список на все пулы - на странице правил." />
  </div>
  <UiBanner
    v-if="disabled"
    kind="warn"
    text="Исключения временно выключены: разбираемся со странным ростом нагрузки на CPU"
  />
  <div
    v-if="list.length"
    class="excllist"
  >
    <span
      v-for="(e, i) in list"
      :key="e"
      class="exclitem"
    >
      {{ e }}
      <button
        type="button"
        class="exclrm"
        title="убрать из списка"
        :disabled="disabled"
        @click="exclRemove(i)"
      >✕</button>
    </span>
  </div>
  <div class="hrow">
    <UiInput
      v-model="draft"
      bare
      placeholder="IP или подсеть; можно несколько через запятую/пробел"
      :disabled="disabled"
      @keydown.enter="exclAdd"
    />
    <UiButton
      :disabled="disabled"
      @click="exclAdd"
    >
      + добавить
    </UiButton>
  </div>
</template>

<style scoped>
.hrow {
  display: flex;
  gap: 6px;
  align-items: flex-start;
  margin-top: 4px;
}
.hrow > :first-child {
  flex: 1;
  min-width: 0;
}
.sect {
  font-weight: 600;
  font-size: 13px;
  margin: 12px 0 4px;
}
.excllist {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.exclitem {
  display: inline-flex;
  gap: 6px;
  align-items: center;
  font-size: 12px;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 3px 8px;
}
.exclrm {
  background: none;
  border: 0;
  color: var(--dim);
  cursor: pointer;
  padding: 0 2px;
  font-size: 12px;
}
.exclrm:hover {
  color: var(--err);
}
</style>
