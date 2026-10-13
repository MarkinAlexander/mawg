// Статус панели/пулов: GET /status каждые 5с + GET /magitrickle каждые 30с
// (как legacy refresh()/refreshMT() из startPanel). Пулы - часть payload'а,
// отдельный стор не нужен (usePools() - срез).
import { computed, reactive } from 'vue'
import { UNAUTHORIZED_EVENT, api } from '../api/client'
import type { MagitrickleRes, PoolView, Status } from '../api/types'

export type MtState = 'checking' | 'yes' | 'no';

const state = reactive({
  loaded: false,
  authNeeded: false,
  error: '' as string,
  mt: 'checking' as MtState,
  data: null as Status | null,
});

let timer: ReturnType<typeof setInterval> | null = null;
let mtTimer: ReturnType<typeof setInterval> | null = null;

export function useStatus() {
  async function refresh(): Promise<void> {
    try {
      const data = await api<Status>('GET', '/status');
      state.data = data;
      state.loaded = true;
      state.error = '';
      state.authNeeded = false;
    } catch (e) {
      // держим предыдущее состояние, текст ошибки - в мету шапки
      // (legacy refresh: #meta = 'ошибка: ' + e.message)
      state.error = e instanceof Error ? e.message : String(e);
    }
  }
  async function refreshMT(): Promise<void> {
    try {
      const r = await api<MagitrickleRes>('GET', '/magitrickle');
      state.mt = r.available ? 'yes' : 'no';
    } catch {
      state.mt = 'no';
    }
  }
  function start(intervalMs = 5000, mtIntervalMs = 30000) {
    void refresh();
    void refreshMT();
    if (timer || mtTimer) return;
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
    timer = setInterval(() => void refresh(), intervalMs);
    mtTimer = setInterval(() => void refreshMT(), mtIntervalMs);
  }
  function stop() {
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
    if (mtTimer) {
      clearInterval(mtTimer);
      mtTimer = null;
    }
    window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
  }
  return { state, refresh, refreshMT, start, stop };
}

function onUnauthorized() {
  state.authNeeded = true;
  state.loaded = false;
}

const pools = computed<PoolView[]>(() => state.data?.pools ?? []);

// срез «пулы» для карточек (legacy читает STATUS.pools)
export function usePools() {
  return { pools };
}
