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

// Липкая шапка на десктопе (просьба пользователя: листаешь - панель
// остаётся сверху, вкладки и поиск всегда под рукой): при скролле
// сжимается в одну строку - строка меты прячется, паддинги меньше.
const scrolled = ref(false);
const onScroll = () => {
  scrolled.value = window.scrollY > 8;
};
onMounted(() => window.addEventListener('scroll', onScroll, { passive: true }));
onBeforeUnmount(() => window.removeEventListener('scroll', onScroll));

// Мобильное меню: НЕ в шапке (растягивало её), а кнопкой внизу справа
// + шторка снизу; жест - потянуть от правого края влево. Закрытие -
// тап по фону, по вкладке, смена маршрута, Escape.
const route = useRoute();
const menuOpen = ref(false);
// закрытие меню ПЕРЕХОДОМ (клик по вкладке в шторке): барьер истории
// остаётся позади новой страницы - Back с неё попадёт на старую, как и
// должно; history.back() здесь нельзя - он откатит только что сделанный
// роутером pushState (кейс: «не переключает ни на какую страницу»)
let menuClosedByRoute = false;
watch(
  () => route.path,
  () => {
    if (menuOpen.value) {
      menuClosedByRoute = true;
      menuOpen.value = false;
    }
  },
);

// кнопка меню: смахивание убирает/возвращает её жестами у правого края
// (67-док §3.7, слово владельца 2026-10-16: FAB перекрывал подсказки
// [?] на телефоне; единственный механизм - края, без драга самой кнопки
// и без тостов про перезагрузку). fabGone - в памяти компонента.
const fabGone = ref(false);

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') menuOpen.value = false;
}
// флаг барьера вместо чтения history.state: его может снести чужой
// pushState (в т.ч. роутера) - локальный флаг надёжен и тестируем
let historyBarrier = false;
watch(menuOpen, (open) => {
  if (open) {
    document.addEventListener('keydown', onKeydown);
    // барьер для кнопки «Назад» (67-док §3.8): Back закрывает меню, а не
    // уходит со страницы; pushState с тем же URL - перехода нет
    try {
      history.pushState({ mawgMenu: true }, '');
      historyBarrier = true;
    } catch {
      // нет истории - перехват Back просто не работает, не критично
    }
    window.addEventListener('popstate', onPopState);
  } else {
    document.removeEventListener('keydown', onKeydown);
    window.removeEventListener('popstate', onPopState);
    // закрыли сами (тап по фону/жест/Esc/кнопка) - снять барьер своим
    // history.back (вернёт на текущую запись - останемся на странице);
    // при закрытии ПЕРЕХОДОМ back() нельзя - откатит роутерный pushState
    if (historyBarrier && !menuClosedByRoute) {
      historyBarrier = false;
      history.back();
    }
    historyBarrier = false;
    menuClosedByRoute = false;
  }
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

// краевые полосы жестов (67-док §3.8+§3.10, слово владельца: Firefox -
// меню открывалось, но кнопка не пряталась; Brave/Chromium - не работал
// даже жест: браузеры забирают краевой свайп под скролл/навигацию и шлют
// pointercancel; потом выяснилось: задвигающий свайп у правого края уводит
// ПАЛЕЦ ЗА ЭКРАН - диджитайзер теряет его раньше порога). Итог - ДВЕ
// полосы с touch-action: none, оба жеста движут пальцем ВНУТРЬ экрана:
// правая - вытянуть из-за края (меню + проявить кнопку), левая -
// задвинуть (закрыть шторку + спрятать кнопку). Тап без свайпа
// пересылается содержимому под полосой (elementFromPoint), чтобы кромки
// не стали мёртвыми.
const SWIPE_PX = 40;  // порог срабатывания
let edgeStart: { x: number; y: number } | null = null;
let edgeSwiped = false;
function onEdgeDown(e: PointerEvent): void {
  edgeStart = { x: e.clientX, y: e.clientY };
  edgeSwiped = false;
  (e.currentTarget as HTMLElement)?.setPointerCapture?.(e.pointerId);
}
function onEdgeMoveRight(e: PointerEvent): void {
  if (!edgeStart || edgeSwiped) return;
  const dx = e.clientX - edgeStart.x;
  const dy = e.clientY - edgeStart.y;
  // вытянуть из-за ПРАВОГО края (палец идёт влево, в экран)
  if (dx <= -SWIPE_PX && Math.abs(dx) > Math.abs(dy)) {
    edgeSwiped = true;
    menuOpen.value = true;
    fabGone.value = false;
  }
}
function onEdgeMoveLeft(e: PointerEvent): void {
  if (!edgeStart || edgeSwiped) return;
  const dx = e.clientX - edgeStart.x;
  const dy = e.clientY - edgeStart.y;
  // задвинуть (палец идёт вправо, в экран): шторка закрыта, кнопка спрятана
  if (dx >= SWIPE_PX && Math.abs(dx) > Math.abs(dy)) {
    edgeSwiped = true;
    menuOpen.value = false;
    fabGone.value = true;
  }
}
function onEdgeUp(): void {
  edgeStart = null;
}
// тап по полосе (без свайпа) - прозрачный пропуск клика содержимому
function onEdgeClick(e: MouseEvent): void {
  if (edgeSwiped) return;
  const strip = e.currentTarget as HTMLElement;
  const x = e.clientX;
  const y = e.clientY;
  strip.style.pointerEvents = 'none';
  const target = document.elementFromPoint(x, y);
  strip.style.pointerEvents = '';
  if (target && target !== strip) {
    target.dispatchEvent(new MouseEvent('click', { bubbles: true, clientX: x, clientY: y }));
  }
}

// клик по шторке: по ссылке - закрытие переходом (роутер сам меняет
// историю, барьер не трогаем - иначе back() отменит переход); по ссылке
// на ТЕКУЩУЮ вкладку перехода не будет - обычное закрытие со снятием
// барьера; по фону/заголовку - то же
function onSheetNavClick(e: MouseEvent): void {
  const a = (e.target as HTMLElement | null)?.closest?.('a') as HTMLAnchorElement | null;
  menuClosedByRoute = !!a && a.getAttribute('href') !== window.location.hash;
  menuOpen.value = false;
}

// кнопка «Назад» при открытом меню закрывает меню, а не уводит со
// страницы (слово владельца): при открытии кладём барьер в историю
// (pushState с тем же URL), Back съедает барьер и закрывает шторку;
// закрыли сами (тап/жест/Esc/вкладка) - барьер снимаем history.back()
// (URL тот же - перехода нет).
function onPopState(): void {
  historyBarrier = false; // барьер съел браузер (Back)
  if (menuOpen.value) menuOpen.value = false;
}
onMounted(() => {
  document.addEventListener('keydown', onSearchKeydown);
  document.addEventListener('click', onSecretClick, true);
  document.addEventListener('keydown', onSecretKeydown);
});
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onSearchKeydown);
  document.removeEventListener('keydown', onKeydown);
  document.removeEventListener('click', onSecretClick, true);
  document.removeEventListener('keydown', onSecretKeydown);
  window.removeEventListener('popstate', onPopState);
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
    <header
      v-if="appVisible"
      :class="{ scrolled }"
    >
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
    <!-- краевые полосы жестов (только мобильная ширина): правая -
         вытянуть меню (и проявить кнопку), левая - задвинуть (закрыть
         шторку и спрятать кнопку); тапы пересылаются содержимому -->
    <div
      v-if="appVisible"
      class="edge-zone right"
      @pointerdown="onEdgeDown"
      @pointermove="onEdgeMoveRight"
      @pointerup="onEdgeUp"
      @pointercancel="onEdgeUp"
      @click="onEdgeClick"
    />
    <div
      v-if="appVisible"
      class="edge-zone left"
      @pointerdown="onEdgeDown"
      @pointermove="onEdgeMoveLeft"
      @pointerup="onEdgeUp"
      @pointercancel="onEdgeUp"
      @click="onEdgeClick"
    />
    <ToastHost />
    <ConfirmHost />
    <SearchDialog
      v-if="appVisible"
      v-model:open="searchOpen"
    />

    <!-- мобильное меню: кнопка внизу справа + шторка снизу (в шапке места
         не занимает и вёрстку не двигает); на десктопе скрыты media-query.
         Кнопку прячут/проявляют жесты у правого края (67-док §3.7) -->
    <button
      v-if="appVisible && !fabGone"
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
          @click="onSheetNavClick"
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
/* липкая шапка - только десктоп (мобильный - FAB и шторка): прижата
   к верху, при скролле сжимается в одну строку без меты, с тенью */
@media (min-width: 821px) {
  header {
    position: sticky;
    top: 0;
    z-index: 40;
    background: var(--bg);
  }
  header.scrolled {
    gap: 10px;
    padding: 6px 10px;
    box-shadow: 0 8px 18px rgba(0, 0, 0, 0.35);
  }
  header.scrolled .meta {
    display: none;
  }
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
/* краевые полосы жестов: на десктопе скрыты; touch-action none -
   браузеру запрещено отбирать свайп под скролл/навигацию (Brave/Firefox);
   обе НАД шторкой (84): задвигать её жестом от левого края */
.edge-zone {
  display: none;
}
@media (max-width: 820px) {
  .edge-zone {
    display: block;
    position: fixed;
    top: 0;
    bottom: 0;
    width: 24px;
    z-index: 86;
    touch-action: none;
  }
  .edge-zone.right {
    right: 0;
  }
  .edge-zone.left {
    left: 0;
  }
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
