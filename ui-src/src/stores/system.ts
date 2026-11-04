// Системная проверка зависимостей: GET /system/check (сервер кэширует
// ответ на 3 минуты, так что частый опрос дешёв).
import { reactive } from 'vue'
import { api } from '../api/client'
import type { CoreDownloadsRes, CoreInfo, EnginesDiagRes, InstallRes, SingboxSourceInfo, SystemCheckRes, UpdateInfo } from '../api/types'

const state = reactive<{ loaded: boolean; data: SystemCheckRes | null }>({ loaded: false, data: null });
const cores = reactive<{ loaded: boolean; list: CoreInfo[] }>({ loaded: false, list: [] });
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
  async function refreshCores(): Promise<void> {
    try {
      const data = await api<{ cores: CoreInfo[] }>('GET', '/cores');
      cores.list = data.cores || [];
      cores.loaded = true;
    } catch {
      // сеть - держим предыдущее
    }
  }
  function start(intervalMs = 60000) {
    void refresh();
    void refreshCores();
    if (timer) return;
    timer = setInterval(() => {
      void refresh();
      void refreshCores();
    }, intervalMs);
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
  // источники установки ядра sing-box (живой дискавери, кэш 10 мин)
  async function coreSources(): Promise<SingboxSourceInfo[]> {
    try {
      const data = await api<{ sources: SingboxSourceInfo[] }>('GET', '/cores/singbox/sources');
      return data.sources || [];
    } catch {
      return [];
    }
  }
  // установка компонента идёт до пары минут - raw fetch, БЕЗ 90с-таймаута
  // api() (легаси тоже ходил сырым fetch)
  async function install(payload: { id: string; flavor?: string; source?: string; backup?: boolean; replace?: boolean }): Promise<InstallRes> {
    const r = await fetch('/api/v1/system/install', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    return (await r.json()) as InstallRes;
  }
  async function setSingboxMode(mode: string): Promise<{ mode?: string; warning?: string }> {
    return api('POST', '/singbox/mode', { mode });
  }
  async function setSingboxCache(mode: 'off' | 'tmp'): Promise<void> {
    await api('POST', '/singbox/cache-file', { mode });
  }
  // рестарт активного MagiTrickle (платформенно: Кинетик mtc/S99,
  // OpenWrt mtc/magitrickle) - кнопка на вкладке Система
  async function mtRestart(): Promise<void> {
    await api('POST', '/system/mt-restart', {});
  }
  // lifecycle ядра: start|stop|restart|enable|disable|uninstall
  async function corePower(id: string, action: string): Promise<CoreInfo> {
    const r = await api<{ core: CoreInfo }>('POST', `/cores/${id}/power`, { action });
    const i = cores.list.findIndex((c) => c.id === id);
    if (i >= 0) cores.list[i] = r.core;
    else cores.list.push(r.core);
    return r.core;
  }
  // заливка своего бинаря движка (112-док 4.1): multipart, конвейер
  // замены с рестартом движка - сырой fetch без 90с-таймаута
  async function uploadCoreBinary(id: string, file: File, backup: boolean): Promise<InstallRes> {
    const form = new FormData();
    form.append('file', file, file.name);
    if (!backup) form.append('backup', '0');
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), 8 * 60_000);
    try {
      const r = await fetch(`/api/v1/cores/${id}/binary`, { method: 'POST', body: form, signal: ctrl.signal });
      return (await r.json()) as InstallRes;
    } finally {
      clearTimeout(timer);
    }
  }
  // прямые ссылки на релизные ассеты под арх роутера (кэш 10 мин)
  async function coreDownloads(): Promise<CoreDownloadsRes | null> {
    try {
      return await api<CoreDownloadsRes>('GET', '/cores/downloads');
    } catch {
      return null;
    }
  }
  // диагностика движков: живость/версии/хвосты логов
  async function enginesDiag(): Promise<EnginesDiagRes | null> {
    try {
      return await api<EnginesDiagRes>('GET', '/system/engines');
    } catch {
      return null;
    }
  }
  return { state, cores, upd, refresh, refreshCores, start, stop, checkUpdate, coreSources, install, setSingboxMode, setSingboxCache, corePower, mtRestart, uploadCoreBinary, coreDownloads, enginesDiag };
}
