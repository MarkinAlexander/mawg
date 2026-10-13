// Интерфейсы роутера + WAN-проба: GET /ifaces каждые 30с (легаси
// startPanel: setInterval(refreshIfaces, 30000)), /wanprobe - при открытии
// вкладки и после сохранения (легаси refreshWAN поллинга не имеет).
// Мутации - тонкие обёртки: вызов API + перечитывание списка.
import { reactive } from 'vue'
import { api } from '../api/client'
import type { IfaceInfo, ProbeConfig } from '../api/types'

export type ProbeBody = ProbeConfig | { target: string }

interface IfacesState {
  loaded: boolean
  error: string // легаси: «интерфейсы недоступны: …» - только пока данных нет
  list: IfaceInfo[]
  wan: { enabled: boolean; probe: ProbeConfig | null }
  wanLoaded: boolean
}

const state = reactive<IfacesState>({
  loaded: false,
  error: '',
  list: [],
  wan: { enabled: false, probe: null },
  wanLoaded: false,
});

let timer: ReturnType<typeof setInterval> | null = null;

export function useIfaces() {
  async function refresh(): Promise<void> {
    try {
      const r = await api<{ interfaces: IfaceInfo[] }>('GET', '/ifaces');
      state.list = r.interfaces || [];
      state.loaded = true;
      state.error = '';
    } catch (e) {
      // легаси: с данными - держим предыдущее и console.warn,
      // без данных - показываем текст ошибки
      if (!state.loaded) state.error = e instanceof Error ? e.message : String(e);
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

  // external <-> hidden (легаси btn.onclick в renderIfaces)
  async function setMode(device: string, mode: 'external' | 'hidden'): Promise<void> {
    await api('POST', `/ifaces/${encodeURIComponent(device)}/mode`, { mode });
    await refresh();
  }

  // сохранение/уборка пробы интерфейса (легаси ipSave/ipRemove)
  async function setProbe(device: string, probe: ProbeBody): Promise<void> {
    await api('PUT', `/ifaces/${encodeURIComponent(device)}/probe`, probe);
    await refresh();
  }

  async function refreshWan(): Promise<void> {
    try {
      const r = await api<{ enabled: boolean; probe: ProbeConfig | null }>('GET', '/wanprobe');
      state.wan = { enabled: !!r.enabled, probe: r.probe || null };
      state.wanLoaded = true;
    } catch {
      // легаси refreshWAN: catch {}
    }
  }

  // {target:''} = выключить (легаси wanSave); после - перечитывание,
  // форму заполняет watch во вьюхе
  async function saveWan(body: ProbeBody): Promise<void> {
    await api('PUT', '/wanprobe', body);
    await refreshWan();
  }

  return { state, refresh, start, stop, setMode, setProbe, refreshWan, saveWan };
}
