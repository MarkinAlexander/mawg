<script setup lang="ts">
// Фаза 0: заглушка вкладки. Паритетный перенос карточек - отдельные серии
// коммитов (порядок и чек-листы в local-docs/44-ui-rewrite-plan.md, Фаза 3).
defineProps<{ title: string }>()
</script>

<template>
  <section class="card">
    <h3 style="margin-top: 0">
      {{ title }}
    </h3>
    <p class="muted">
      Вкладка на переносе: функционал пока в
      <RouterLink to="/">
        legacy-панели
      </RouterLink>.
    </p>
  </section>
</template>
