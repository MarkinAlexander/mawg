<script setup lang="ts">
import { useRoute } from 'vue-router'

const route = useRoute()
</script>

<template>
  <p class="muted">Страница «{{ route.params.page }}» появится при переносе панели. Старая - <a href="/">/</a>.</p>
</template>

<style scoped>
.muted { color: #7c8590; }
</style>
