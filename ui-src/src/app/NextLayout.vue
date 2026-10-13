<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { ConfirmHost, ToastHost, UiBanner } from '../components/ui'
import { inflight } from '../api/client'
import { useStatus, type MtState } from '../stores/status'
import { TABS } from './tabs'
import UpdateBadge from './UpdateBadge.vue'

// Легаси-замок: panel.css прячет header/main до авторизации
// (body.unlocked ставит/снимает panel.js при проверке входа). На /next/
// panel.js не запускается - новое UI объявляет себя разблокированным
// само; настоящая авторизация для нового UI - Фаза 5.
onMounted(() => document.body.classList.add('unlocked'))
onBeforeUnmount(() => document.body.classList.remove('unlocked'))

// Шапка общая для всех вкладок - глобальные опросы статуса живут здесь,
// как легаси startPanel: GET /status каждые 5с + GET /magitrickle 30с.
const { state: status, start, stop } = useStatus();
onMounted(start);
onBeforeUnmount(stop);

const MT_LABEL: Record<MtState, string> = {
  checking: 'проверяю...',
  yes: 'доступен',
  no: 'недоступен',
};
// легаси updateMeta/refresh: ошибка последнего опроса заменяет мету целиком
const meta = computed(() => {
  if (status.error) return 'ошибка: ' + status.error;
  if (!status.data) return 'загрузка...';
  return `${status.data.platform} | mawg ${status.data.version} | magitrickle: ${MT_LABEL[status.mt]}`;
});
</script>

<template>
  <div class="nextapp">
    <!-- индикатор in-flight запросов (легаси #topbar) -->
    <div
      class="topbar"
      :class="{ on: inflight.n > 0 }"
    />
    <header>
      <h1>
        mawg <span
          class="badge"
          title="новая версия интерфейса, живёт за /next до конца Фазы 3"
        >next</span>
      </h1>
      <span class="meta">{{ meta }}</span>
      <UpdateBadge />
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
      <!-- мост до Фазы 5: реальный оверлей логина умеет только legacy -->
      <UiBanner
        v-if="status.authNeeded"
        kind="err"
        text="требуется вход: сессия не действует - войдите через старую версию, затем вернитесь"
      >
        <RouterLink to="/">
          открыть старую версию
        </RouterLink>
      </UiBanner>
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
.meta {
  color: var(--dim);
  font-size: 12px;
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
/* легаси #topbar: имена topbar/nslide не пересекаются с глобальным panel.css */
.topbar {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  height: 3px;
  z-index: 999;
  pointer-events: none;
  opacity: 0;
  transition: opacity 0.25s;
  overflow: hidden;
}
.topbar.on {
  opacity: 1;
}
.topbar.on::before {
  content: '';
  position: absolute;
  left: -40%;
  width: 40%;
  height: 100%;
  background: var(--accent);
  animation: nslide 1s ease-in-out infinite;
}
@keyframes nslide {
  to {
    left: 100%;
  }
}
</style>
