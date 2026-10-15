<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ConfirmHost, ToastHost, UiButton, UiIcon } from '../components/ui'
import { inflight } from '../api/client'
import { useStatus, type MtState } from '../stores/status'
import { usePrivacy } from '../stores/privacy'
import { TABS } from './tabs'
import SearchDialog from './SearchDialog.vue'
import LoginDialog from './LoginDialog.vue'
import UpdateBadge from './UpdateBadge.vue'

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

// Поиск по настройкам (46-док §1): лупа в шапке + Ctrl+K / Cmd+K
const searchOpen = ref(false);
function onSearchKeydown(e: KeyboardEvent) {
  if (!appVisible.value) return;
  if ((e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === 'k') {
    e.preventDefault();
    searchOpen.value = true;
  }
}

// Приватный режим (62-док): eye/eye-off рядом с лупой, состояние - в стор
// (localStorage mawg-privacy). Класс privacy-on на body навешивается здесь
// (шейка видна на всех вкладках); при выключении снимаем и проявления полей.
const privacy = usePrivacy();
watch(
  () => privacy.state.on,
  (on) => {
    document.body.classList.toggle('privacy-on', on);
    if (!on) {
      document.querySelectorAll('.secret-open').forEach((n) => n.classList.remove('secret-open'));
    }
  },
  { immediate: true },
);

// Проявление по клику (3x-ui-стиль): capture-клик по документу, клик по
// .secret добавляет узлу secret-open (blur снят), клик мимо или повторный
// клик по тому же полю - размывает снова. Глобальный режим не переключается -
// проявляется только конкретное поле.
function onSecretClick(e: MouseEvent) {
  const el = (e.target as HTMLElement | null)?.closest?.('.secret') as HTMLElement | null;
  const opened = document.querySelectorAll('.secret-open');
  if (!el) {
    opened.forEach((n) => n.classList.remove('secret-open'));
    return;
  }
  const wasOpen = el.classList.contains('secret-open');
  opened.forEach((n) => n.classList.remove('secret-open'));
  if (!wasOpen) el.classList.add('secret-open');
}

// Escape - снять все проявления (как клик мимо)
function onSecretKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    document.querySelectorAll('.secret-open').forEach((n) => n.classList.remove('secret-open'));
  }
}

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
  document.addEventListener('keydown', onSearchKeydown);
  document.addEventListener('click', onSecretClick, true);
  document.addEventListener('keydown', onSecretKeydown);
  document.addEventListener('pointerdown', onPointerDown);
  document.addEventListener('pointermove', onPointerMove);
  document.addEventListener('pointerup', onPointerUp);
  document.addEventListener('pointercancel', onPointerUp);
});
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onSearchKeydown);
  document.removeEventListener('keydown', onKeydown);
  document.removeEventListener('click', onSecretClick, true);
  document.removeEventListener('keydown', onSecretKeydown);
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

// Гейт входа (слово владельца 2026-10-15): при включённой авторизации за
// диалогом входа страница не рендерится - ни шапка, ни вкладки. Контент
// скрыт, пока требуется вход (authNeeded) и пока статус не известен
// (!loaded без ошибки - иначе была бы вспышка страницы до первого /status,
// как легаси-замок body:not(.unlocked)). Ошибка сети - показываем каркас:
// сервер недоступен, вход ни при чём.
const appVisible = computed(() => !status.authNeeded && (status.loaded || !!status.error));
</script>

<template>
  <div class="nextapp">
    <!-- индикатор in-flight запросов (легаси #topbar) -->
    <div
      class="topbar"
      :class="{ on: inflight.n > 0 }"
    />
    <header v-if="appVisible">
      <h1>
        <RouterLink
          to="/next/"
          class="brand"
          title="главная новой панели"
        >
          mawg <span
            class="badge"
            title="новая версия интерфейса"
          >next</span>
        </RouterLink>
      </h1>
      <span class="meta">{{ meta }}</span>
      <UpdateBadge />
      <UiButton
        variant="icon"
        class="searchbtn"
        tooltip="поиск по настройкам (Ctrl+K)"
        @click="searchOpen = true"
      >
        <UiIcon name="search" />
      </UiButton>
      <!-- приватный режим (62-док): глобальный тумблер размытия; иконка -
           текущее состояние, tooltip - что сделает клик + честная оговорка,
           что скрытие визуальное -->
      <UiButton
        variant="icon"
        class="privacybtn"
        :class="{ on: privacy.state.on }"
        :tooltip="privacy.state.on
          ? 'показать (данные размыты) - скрываются адреса из конфигов и секреты; визуальное скрытие для скриншотов и демонстрации'
          : 'скрыть адреса туннелей (из конфигов) и секреты - визуальное скрытие для скриншотов и демонстрации'"
        @click="privacy.toggle()"
      >
        <UiIcon :name="privacy.state.on ? 'eye-off' : 'eye'" />
      </UiButton>
      <nav>
        <RouterLink
          v-for="t in TABS"
          :key="t.id"
          :to="`/next/${t.id}`"
        >
          {{ t.label }}
        </RouterLink>
      </nav>
    </header>
    <!-- гейт входа: при включённой авторизации за диалогом страница не
         рендерится (слово владельца) - LoginDialog вне main, контент
         размонтируется, а не прячется под оверлеем -->
    <LoginDialog :open="status.authNeeded" />
    <main v-if="appVisible">
      <RouterView />
    </main>
    <ToastHost />
    <ConfirmHost />
    <SearchDialog
      v-if="appVisible"
      v-model:open="searchOpen"
    />

    <!-- мобильное меню: кнопка внизу справа + шторка снизу (в шапке места
         не занимает и вёрстку не двигает); на десктопе скрыты media-query -->
    <button
      v-if="appVisible"
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
        v-if="appVisible && menuOpen"
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
/* приватный режим включён - кнопка подсвечена (62-док) */
.privacybtn.on {
  color: var(--warn);
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
    width: 42px;
    height: 42px;
    /* квадрат со скруглением - в стиле кнопок карточек; зелёный как
       активный пункт меню (слово владельца) */
    border-radius: 8px;
    flex-direction: column;
    justify-content: center;
    align-items: center;
    gap: 4px;
    padding: 0;
    background: var(--ok);
    border: none;
    box-shadow: 0 2px 10px rgba(0, 0, 0, 0.4);
  }
  .menu-fab span {
    display: block;
    width: 18px;
    height: 2px;
    border-radius: 1px;
    background: #fff;
  }
  /* кнопка меню fixed внизу справа: пока она видна, странице нужен
     нижний паддинг (42px кнопка + 16px отступ + 12px запас), чтобы она
     не перекрывала контент при прокрутке. Слово владельца: горизонтальный
     отступ 4px - на телефоне контент плотнее */
  .nextapp {
    padding: 0 4px calc(70px + env(safe-area-inset-bottom));
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
/* in-flight-индикатор: имена topbar/nslide исторически разведены с легаси,
   теперь и с глобальной base.css не пересекаются */
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
