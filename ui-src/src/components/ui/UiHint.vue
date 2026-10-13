<script setup lang="ts">
// Серая инлайн-подсказка - единый стиль пояснений (legacy: .muted 11px).
defineProps<{
  text: string
}>()
</script>

<template>
  <div class="uihint">
    {{ text }}
  </div>
</template>

<style scoped>
.uihint {
  color: var(--dim);
  font-size: 11px;
  margin-top: 2px;
}
</style>
