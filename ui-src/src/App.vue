<script setup lang="ts">
</script>

<template>
  <div class="shell">
    <header>
      <b>mawg ui</b>
      <span class="muted">новая панель (в разработке), старая - <a href="/">/</a></span>
    </header>
    <nav>
      <RouterLink to="/">Статус</RouterLink>
      <RouterLink to="/pools">Пулы (позже)</RouterLink>
      <RouterLink to="/magitrickle">МагиТрикл (позже)</RouterLink>
    </nav>
    <main>
      <RouterView />
    </main>
  </div>
</template>

<style>
body { margin: 0; font: 14px/1.5 system-ui, sans-serif; background: #111418; color: #d8dce2; }
.shell { max-width: 1100px; margin: 0 auto; padding: 12px; }
header { display: flex; gap: 12px; align-items: baseline; padding: 6px 0; }
.muted { color: #7c8590; font-size: 12px; }
nav { display: flex; gap: 14px; padding: 6px 0; border-bottom: 1px solid #2a2f36; }
nav a { color: #9ecbff; text-decoration: none; }
nav a.router-link-active { color: #fff; font-weight: 600; }
main { padding-top: 12px; }
table { border-collapse: collapse; width: 100%; }
th, td { text-align: left; padding: 4px 8px; border-bottom: 1px solid #2a2f36; }
th { color: #7c8590; font-weight: normal; }
.err { color: #ff8484; white-space: pre-wrap; }
</style>
