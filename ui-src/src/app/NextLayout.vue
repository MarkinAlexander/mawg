<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
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

// Мобильное меню (бургер): на узких экранах nav прячется за кнопкой.
// Закрытие - по любому клику внутри меню и по смене маршрута.
const route = useRoute();
const menuOpen = ref(false);
watch(
  () => route.path,
  () => {
    menuOpen.value = false;
  },
);

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
        <RouterLink
          to="/next/"
          class="brand"
          title="главная новой панели"
        >
          mawg <span
            class="badge"
            title="новая версия интерфейса, живёт за /next до конца Фазы 3"
          >next</span>
        </RouterLink>
      </h1>
      <span class="meta">{{ meta }}</span>
      <UpdateBadge />
      <button
        type="button"
        class="burger"
        :aria-expanded="menuOpen"
        aria-label="меню"
        title="вкладки"
        @click="menuOpen = !menuOpen"
      >
        <span /><span /><span />
      </button>
      <nav
        :class="{ open: menuOpen }"
        @click="menuOpen = false"
      >
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
  padding: 10px;
  border-bottom: 1px solid var(--border);
}
header h1 {
  margin: 0;
  font-size: 20px;
}
.brand {
  color: inherit;
  text-decoration: none;
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
/* бургер: скрыт на широких экранах, nav прячется за ним на узких */
.burger {
  display: none;
}
@media (max-width: 820px) {
  .burger {
    display: inline-flex;
    flex-direction: column;
    justify-content: center;
    align-self: center;
    gap: 4px;
    width: 38px;
    height: 32px;
    padding: 0 9px;
    margin-left: auto;
  }
  .burger span {
    display: block;
    width: 100%;
    height: 2px;
    border-radius: 1px;
    background: var(--text);
  }
  nav {
    display: none;
    flex-basis: 100%;
    flex-direction: column;
    gap: 2px;
    border-top: 1px solid var(--border);
    padding-top: 6px;
  }
  nav.open {
    display: flex;
  }
  nav a {
    padding: 9px 10px;
  }
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
