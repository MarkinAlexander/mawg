// Статус панели/пулов: GET /status каждые 5с (как legacy refresh()).
// Пулы - часть payload'а, отдельный стор не нужен (usePools() - срез).
import { computed, reactive } from 'vue'
import { UNAUTHORIZED_EVENT, api } from '../api/client'
import type { PoolView, Status } from '../api/types'

const state = reactive({
  loaded: false,
  authNeeded: false,
  data: null as Status | null,
});

let timer: ReturnType<typeof setInterval> | null = null;

export function useStatus() {
  async function refresh(): Promise<void> {
    try {
      const data = await api<Status>('GET', '/status');
      state.data = data;
      state.loaded = true;
      state.authNeeded = false;
    } catch {
      // сеть/таймаут - держим предыдущее состояние; 401 замораживает
      // вызов внутри клиента и помечается событием
    }
  }
  function start(intervalMs = 5000) {
    void refresh();
    if (timer) return;
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
    timer = setInterval(() => void refresh(), intervalMs);
  }
  function stop() {
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
    window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
  }
  return { state, refresh, start, stop };
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
