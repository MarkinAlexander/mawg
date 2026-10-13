<script setup lang="ts">
// Страничный таб-переключатель внутри карточек (навигация вкладок
// приложения остаётся роутерной в NextLayout). hint - в title кнопки.
export interface UiTabDef {
  id: string
  label: string
  hint?: string
}

defineProps<{
  tabs: UiTabDef[]
  modelValue: string
}>()

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <div
    class="uitabs"
    role="tablist"
  >
    <button
      v-for="t in tabs"
      :key="t.id"
      type="button"
      role="tab"
      class="tab"
      :class="{ on: t.id === modelValue }"
      :aria-selected="t.id === modelValue"
      :title="t.hint"
      @click="emit('update:modelValue', t.id)"
    >
      {{ t.label }}
    </button>
  </div>
</template>

<style scoped>
.uitabs {
  display: flex;
  flex-wrap: wrap;
  gap: 2px;
  margin: 6px 0 10px;
}
.tab {
  padding: 4px 10px;
  border-radius: 8px;
  background: none;
  border: none;
  color: var(--text);
  font-size: 13px;
  cursor: pointer;
}
.tab:hover {
  background: var(--panel2);
}
.tab.on {
  background: var(--panel);
  font-weight: 600;
}
.tab:not(.on) {
  border: 1px solid transparent;
}
</style>
