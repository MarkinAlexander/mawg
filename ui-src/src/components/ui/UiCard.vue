<script setup lang="ts">
// Карточка страницы (legacy .card): заголовок с action-слотом справа,
// серый подзаголовок, опциональная сворачиваемость (паритет details-
// секций групп правил).
const props = withDefaults(
  defineProps<{
    title: string
    subtitle?: string
    collapsible?: boolean
    collapsed?: boolean
  }>(),
  { collapsible: false, collapsed: false, subtitle: undefined },
)

const emit = defineEmits<{ 'update:collapsed': [value: boolean] }>()

function toggle() {
  emit('update:collapsed', !props.collapsed)
}
</script>

<template>
  <section class="card">
    <h2>
      <span class="ttl">
        <button
          v-if="props.collapsible"
          type="button"
          class="fold"
          :title="props.collapsed ? 'развернуть' : 'свернуть'"
          @click="toggle"
        >
          {{ props.collapsed ? '▸' : '▾' }}
        </button>
        {{ props.title }}
      </span>
      <span class="actions"><slot name="actions" /></span>
    </h2>
    <div
      v-if="props.subtitle"
      class="sub"
    >
      {{ props.subtitle }}
    </div>
    <div
      v-show="!props.collapsed"
      class="body"
    >
      <slot />
    </div>
  </section>
</template>

<style scoped>
.card {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 14px 20px;
  margin-bottom: 16px;
}
.card h2 {
  margin: 0 0 4px;
  font-size: 15px;
  display: flex;
  align-items: center;
  gap: 8px;
  justify-content: space-between;
}
.ttl {
  display: flex;
  align-items: center;
  gap: 4px;
}
.fold {
  background: none;
  border: none;
  color: var(--dim);
  cursor: pointer;
  padding: 0 4px;
  font-size: 13px;
}
.fold:hover {
  color: var(--text);
}
.actions {
  display: flex;
  gap: 6px;
  align-items: center;
  flex-wrap: wrap;
}
.sub {
  color: var(--dim);
  font-size: 12px;
  margin-bottom: 10px;
}
</style>
