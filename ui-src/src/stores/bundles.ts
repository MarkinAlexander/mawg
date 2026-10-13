// Наборы интерфейсов (легаси refreshBundles 2264/renderBundles 2275):
// GET /bundles каждые 30с таб-локально, мутации - POST/PUT/DELETE + перечитывание.
import { reactive } from 'vue'
import { api } from '../api/client'
import type { BundleView } from '../api/types'

interface BundlesState {
  loaded: boolean
  list: BundleView[]
}

const state = reactive<BundlesState>({ loaded: false, list: [] });

let timer: ReturnType<typeof setInterval> | null = null;

export function useBundles() {
  async function refresh(): Promise<void> {
    try {
      const r = await api<{ bundles: BundleView[] }>('GET', '/bundles');
      state.list = r.bundles || [];
      state.loaded = true;
    } catch {
      // легаси: console.warn, держим предыдущее
    }
  }
  function start(intervalMs = 30000) {
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

  // body: имя (создание/переименование запрещено бэкендом у правки - имя
  // фиксировано), участники по приоритету, группы MagiTrickle
  async function save(editing: string | null, body: { name: string; members: string[]; groups: string[] }): Promise<void> {
    if (editing) await api('PUT', `/bundles/${encodeURIComponent(editing)}`, body);
    else await api('POST', '/bundles', body);
    await refresh();
  }

  async function del(name: string): Promise<void> {
    await api('DELETE', `/bundles/${encodeURIComponent(name)}`);
    await refresh();
  }

  return { state, refresh, start, stop, save, del };
}
