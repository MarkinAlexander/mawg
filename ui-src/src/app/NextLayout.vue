<script setup lang="ts">
import { onBeforeUnmount, onMounted } from 'vue'
import { ConfirmHost, ToastHost } from '../components/ui'
import { TABS } from './tabs'

// Легаси-замок: panel.css прячет header/main до авторизации
// (body.unlocked ставит/снимает panel.js при проверке входа). На /next/
// panel.js не запускается - новое UI объявляет себя разблокированным
// само; настоящая авторизация для нового UI - Фаза 5.
onMounted(() => document.body.classList.add('unlocked'))
onBeforeUnmount(() => document.body.classList.remove('unlocked'))
</script>

<template>
  <div class="nextapp">
    <header>
      <h1>
        mawg <span
          class="badge"
          title="новая версия интерфейса, живёт за /next до конца Фазы 3"
        >next</span>
      </h1>
      <nav>
        <RouterLink
          v-for="t in TABS"
          :key="t.id"
          :to="`/next/${t.id}`"
        >
          {{ t.label }}
        </RouterLink>
        <RouterLink
          to="/"
          title="legacy-панель остаётся рабочей до конца Фазы 3"
        >
          старая версия
        </RouterLink>
      </nav>
    </header>
    <main>
      <RouterView />
    </main>
    <ToastHost />
    <ConfirmHost />
  </div>
</template>

<style scoped>
.nextapp {
  max-width: 1100px;
  margin: 0 auto;
  padding: 0 14px;
}
header {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 12px;
  padding: 10px 0;
  border-bottom: 1px solid var(--border);
}
header h1 {
  margin: 0;
  font-size: 20px;
}
nav {
  display: flex;
  flex-wrap: wrap;
  gap: 2px;
}
nav a {
  padding: 4px 10px;
  border-radius: 8px;
  text-decoration: none;
  color: var(--text);
}
nav a.router-link-active {
  background: var(--panel);
  font-weight: 600;
}
main {
  padding: 12px 0;
}
</style>
