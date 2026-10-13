// Системная проверка зависимостей: GET /system/check (сервер кэширует
// ответ на 3 минуты, так что частый опрос дешёв).
import { reactive } from 'vue'
import { api } from '../api/client'
import type { SystemCheckRes, UpdateInfo } from '../api/types'

const state = reactive<{ loaded: boolean; data: SystemCheckRes | null }>({ loaded: false, data: null });
const upd = reactive<{ info: UpdateInfo | null }>({ info: null });

let timer: ReturnType<typeof setInterval> | null = null;

export function useSystem() {
  async function refresh(): Promise<void> {
    try {
      const data = await api<SystemCheckRes>('GET', '/system/check');
      state.data = data;
      state.loaded = true;
    } catch {
      // сеть - держим предыдущее
    }
  }
  function start(intervalMs = 60000) {
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
  async function checkUpdate(): Promise<UpdateInfo> {
    const info = await api<UpdateInfo>('GET', '/system/update/check');
    upd.info = info;
    return info;
  }
  return { state, upd, refresh, start, stop, checkUpdate };
}
