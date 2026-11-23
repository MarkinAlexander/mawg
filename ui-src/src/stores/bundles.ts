// Наборы интерфейсов (легаси refreshBundles 2264/renderBundles 2275):
// GET /bundles каждые 30с таб-локально, мутации - POST/PUT/DELETE + перечитывание.
import { reactive } from 'vue'
import { api } from '../api/client'
import type { BundleRules, BundleView } from '../api/types'

interface BundlesState {
  loaded: boolean
  list: BundleView[]
  rules: BundleRules | null
}

const state = reactive<BundlesState>({ loaded: false, list: [], rules: null });

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
  async function loadRules(): Promise<void> {
    try {
      state.rules = await api<BundleRules>('GET', '/mt/bundle-rules');
    } catch {
      // ошибки молча: форма ждёт правила
    }
  }
  function start(intervalMs = 30000) {
    void refresh();
    void loadRules();
    if (timer) return;
    timer = setInterval(() => void refresh(), intervalMs);
  }
  function stop() {
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
  }

  // у правки имя фиксировано (бэкенд запрещает переименование);
  // members идут по приоритету
  async function save(editing: string | null, body: { name: string; members: string[]; groups: string[] }): Promise<void> {
    if (editing) await api('PUT', `/bundles/${encodeURIComponent(editing)}`, body);
    else await api('POST', '/bundles', body);
    await refresh();
  }

  async function del(name: string): Promise<void> {
    await api('DELETE', `/bundles/${encodeURIComponent(name)}`);
    await refresh();
  }

  async function saveRules(rules: BundleRules): Promise<BundleRules> {
    state.rules = await api<BundleRules>('PUT', '/mt/bundle-rules', rules);
    return state.rules;
  }

  return { state, refresh, loadRules, start, stop, save, del, saveRules };
}
