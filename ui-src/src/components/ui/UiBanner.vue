<script setup lang="ts">
// Баннер-предупреждение (legacy: .sub с инлайн-цветом - глитчи/враппер/
// обновления). link - опциональная ссылка в конце текста.
defineProps<{
  kind: 'info' | 'warn' | 'err'
  text: string
  link?: { href: string; label: string }
}>()
</script>

<template>
  <div
    class="uibanner"
    :class="`k-${kind}`"
  >
    {{ text }}
    <a
      v-if="link"
      :href="link.href"
      target="_blank"
      rel="noopener"
    >{{ link.label }}</a>
    <slot />
  </div>
</template>

<style scoped>
.uibanner {
  font-size: 12px;
  margin-top: 6px;
  line-height: 1.45;
}
.k-info {
  color: var(--accent);
}
.k-warn {
  color: var(--warn);
}
.k-err {
  color: var(--err);
}
.uibanner a {
  color: inherit;
}
</style>
