// Настройки сервера (адрес/порт/авторизация/разрешённые адреса).
// Семантика legacy: GET при открытии карточки, PUT по кнопке; список
// разрешённых адресов с автосохранением меняется тем же PUT.
import { reactive } from 'vue'
import { api } from '../api/client'
import type { IPAllow, ServerSettings } from '../api/types'

const state = reactive<{ loaded: boolean; data: ServerSettings | null }>({ loaded: false, data: null });

export function useServerSettings() {
  async function load(): Promise<ServerSettings | null> {
    const data = await api<ServerSettings>('GET', '/settings/server');
    state.data = data;
    state.loaded = true;
    return data;
  }
  // сохраняет только переданные поля: бэкенд пустые listenAddr/port
  // заменяет текущими, так что автосохранение списка не задевает сеть
  async function saveAllowed(allowed: IPAllow[]): Promise<IPAllow[]> {
    const r = await api<{ ok: boolean; allowedIps?: IPAllow[] }>('PUT', '/settings/server', { allowedIps: allowed });
    if (r.allowedIps && state.data) state.data.allowedIps = r.allowedIps;
    return r.allowedIps ?? allowed;
  }
  async function save(body: { listenAddr?: string; port?: number; allowedIps?: IPAllow[]; authDisabled?: boolean }): Promise<{ needRestart: boolean }> {
    const r = await api<{ ok: boolean; needRestart: boolean; allowedIps?: IPAllow[] }>('PUT', '/settings/server', body);
    await load();
    return { needRestart: !!r.needRestart };
  }
  return { state, load, save, saveAllowed };
}
