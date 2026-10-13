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

// Мобильное меню: НЕ в шапке (растягивало её), а кнопкой внизу справа
// + шторка снизу; жест - потянуть от правого края влево. Закрытие -
// тап по фону, по вкладке, смена маршрута, Escape.
const route = useRoute();
const menuOpen = ref(false);
watch(
  () => route.path,
  () => {
    menuOpen.value = false;
  },
);

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') menuOpen.value = false;
}
watch(menuOpen, (open) => {
  if (open) document.addEventListener('keydown', onKeydown);
  else document.removeEventListener('keydown', onKeydown);
});

// жест от правого края: pointerdown в 24px от правого края на мобильной
// ширине, тянем влево >40px (и горизонталь сильнее вертикали) - открываем.
// Pointer Events покрывают и палец, и мышь; на десктопе меню скрыто,
// поэтому guard по matchMedia.
const EDGE_PX = 24;
const SWIPE_PX = 40;
let swipeStart: { x: number; y: number } | null = null;
function onPointerDown(e: PointerEvent) {
  if (!window.matchMedia('(max-width: 820px)').matches) return;
  if (e.clientX < window.innerWidth - EDGE_PX) return;
  swipeStart = { x: e.clientX, y: e.clientY };
}
function onPointerMove(e: PointerEvent) {
  if (!swipeStart || menuOpen.value) return;
  const dx = e.clientX - swipeStart.x;
  const dy = e.clientY - swipeStart.y;
  if (dx < -SWIPE_PX && Math.abs(dx) > Math.abs(dy)) {
    menuOpen.value = true;
    swipeStart = null;
  }
  // жест отменён уходом по вертикали (скролл)
  if (Math.abs(dy) > SWIPE_PX) swipeStart = null;
}
function onPointerUp() {
  swipeStart = null;
}
onMounted(() => {
  document.addEventListener('pointerdown', onPointerDown);
  document.addEventListener('pointermove', onPointerMove);
  document.addEventListener('pointerup', onPointerUp);
  document.addEventListener('pointercancel', onPointerUp);
});
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKeydown);
  document.removeEventListener('pointerdown', onPointerDown);
  document.removeEventListener('pointermove', onPointerMove);
  document.removeEventListener('pointerup', onPointerUp);
  document.removeEventListener('pointercancel', onPointerUp);
});

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

    <!-- мобильное меню: кнопка внизу справа + шторка снизу (в шапке места
         не занимает и вёрстку не двигает); на десктопе скрыты media-query -->
    <button
      type="button"
      class="menu-fab"
      :aria-expanded="menuOpen"
      aria-label="меню"
      title="вкладки"
      @click="menuOpen = !menuOpen"
    >
      <span /><span /><span />
    </button>
    <Transition name="sheet">
      <div
        v-if="menuOpen"
        class="menu-wrap"
      >
        <div
          class="sheet-backdrop"
          @click="menuOpen = false"
        />
        <nav
          class="sheet"
          @click="menuOpen = false"
        >
          <div class="sheet-title">
            Вкладки
          </div>
          <RouterLink
            v-for="t in TABS"
            :key="t.id"
            :to="`/next/${t.id}`"
          >
            {{ t.label }}
          </RouterLink>
          <RouterLink
            to="/"
            class="sheet-legacy"
            title="legacy-панель остаётся рабочей до конца Фазы 3"
          >
            старая версия
          </RouterLink>
        </nav>
      </div>
    </Transition>
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
  /* активная вкладка - сразу видно (слово владельца): зелёная заливка,
     тёмный текст; #3fb96a = --ok, тёмный - литерал владельца */
  background: var(--ok);
  font-weight: 600;
  color: #120b0b;
}
main {
  padding: 12px 0;
}
/* мобильное меню: на широких экранах всё скрыто; FAB - круглая кнопка
   внизу справа, меню - шторка снизу на весь экран */
.menu-fab {
  display: none;
}
@media (max-width: 820px) {
  nav {
    display: none;
  }
  .menu-fab {
    display: flex;
    position: fixed;
    right: 16px;
    bottom: calc(16px + env(safe-area-inset-bottom));
    z-index: 80;
    width: 52px;
    height: 52px;
    /* квадрат со скруглением - в стиле кнопок карточек; зелёный как
       активный пункт меню (слово владельца) */
    border-radius: 8px;
    flex-direction: column;
    justify-content: center;
    align-items: center;
    gap: 5px;
    padding: 0;
    background: var(--ok);
    border: none;
    box-shadow: 0 2px 10px rgba(0, 0, 0, 0.4);
  }
  .menu-fab span {
    display: block;
    width: 22px;
    height: 2px;
    border-radius: 1px;
    background: #fff;
  }
  /* кнопка меню fixed внизу справа: пока она видна, странице нужен
     нижний паддинг (52px кнопка + 16px отступ + 12px запас), чтобы она
     не перекрывала контент при прокрутке */
  .nextapp {
    padding-bottom: calc(80px + env(safe-area-inset-bottom));
  }
}
.menu-wrap {
  position: fixed;
  inset: 0;
  z-index: 84;
}
.sheet-backdrop {
  position: absolute;
  inset: 0;
  background: rgba(0, 0, 0, 0.45);
}
.sheet {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  display: flex;
  flex-direction: column;
  background: var(--panel);
  border-top: 1px solid var(--border);
  border-radius: 14px 14px 0 0;
  padding: 8px 10px calc(12px + env(safe-area-inset-bottom));
  box-shadow: 0 -4px 20px rgba(0, 0, 0, 0.4);
}
.sheet-title {
  color: var(--dim);
  font-size: 12px;
  padding: 4px 10px 8px;
}
.sheet a {
  padding: 12px 10px;
  border-radius: 8px;
  text-decoration: none;
  color: var(--text);
  font-size: 15px;
}
.sheet a.router-link-active {
  /* единая композиция с десктопом: зелёная заливка, тёмный текст */
  background: var(--ok);
  font-weight: 600;
  color: #120b0b;
}
.sheet a.sheet-legacy {
  border-top: 1px solid var(--border);
  margin-top: 6px;
  color: var(--dim);
}
/* анимация шторки: фон гаснет, панель выезжает снизу */
.sheet-enter-active,
.sheet-leave-active {
  transition: opacity 0.18s ease;
}
.sheet-enter-active .sheet,
.sheet-leave-active .sheet {
  transition: transform 0.18s ease;
}
.sheet-enter-from,
.sheet-leave-to {
  opacity: 0;
}
.sheet-enter-from .sheet,
.sheet-leave-to .sheet {
  transform: translateY(100%);
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
