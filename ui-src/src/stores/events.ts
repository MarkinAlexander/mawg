// События (events.json): GET /events, опрос 10с. Сервер отдаёт список
// целиком; legacy показывает хвост.
import { reactive } from 'vue'
import { api } from '../api/client'
import type { AppEvent } from '../api/types'

const state = reactive<{ list: AppEvent[] }>({ list: [] });

let timer: ReturnType<typeof setInterval> | null = null;

export function useEvents() {
  async function refresh(): Promise<void> {
    try {
      const r = await api<{ events: AppEvent[] }>('GET', '/events');
      state.list = r.events || [];
    } catch {
      // сеть - держим предыдущее
    }
  }
  function start(intervalMs = 10000) {
    void refresh();
    if (timer) return;
    timer = setInterval(() => void refresh(), intervalMs);
  }
  function stop() {
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
  }
  return { state, refresh, start, stop };
}
