// Генератор WARP-конфигов (77-док): пресеты для диалога, генерация пула
// и добавление конфига в живой варп-пул. Регистрации устройств идут
// последовательно (до минуты на конфиг с живой пробой) - generate идёт
// raw fetch БЕЗ 90с-таймаута api() (как установка ядра в system.ts).
// Ошибки не бросаем: и 200, и 502 несут разобранный план (502 - совет
// zator + сайт-генератор), сетевой сбой - поле error.

export interface WarpSite {
  id: string
  label: string
  count: number
}

export interface WarpPresets {
  domainEndpoint: string
  v4Blocks: string[]
  ports: number[]
  presets: string[]
  sites: WarpSite[]
  limits: { count: number }
}

export interface WarpResultOne {
  file?: string
  endpoint: string
  address?: string
  ok?: boolean
  detail?: string
  error?: string
}

export interface WarpPlan {
  pool?: string
  added?: number
  results?: WarpResultOne[]
  warnings?: string[]
  error?: string
  unreachable?: boolean
  hosts?: string[]
  generator?: string
}

export interface WarpGenerateBody {
  count: number
  mode: 'random' | 'fixed'
  endpoint?: string
  sites?: string[]
  subnets?: string[]
}

import { reactive } from 'vue'

const state = reactive<{ loaded: boolean; presets: WarpPresets | null }>({ loaded: false, presets: null });

async function call(path: string, body: unknown): Promise<WarpPlan> {
  let res: Response;
  try {
    res = await fetch('/api/v1' + path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body ?? {}),
    });
  } catch (e) {
    return { error: e instanceof Error ? e.message : String(e) };
  }
  try {
    return (await res.json()) as WarpPlan;
  } catch {
    return { error: 'сервер ответил не-JSON (HTTP ' + res.status + ')' };
  }
}

export function useWarp() {
  async function refresh(): Promise<void> {
    try {
      const r = await fetch('/api/v1/warp/presets');
      if (!r.ok) return;
      state.presets = (await r.json()) as WarpPresets;
      state.loaded = true;
    } catch {
      // сеть - оставляем как есть, диалог покажет пустые пресеты
    }
  }

  function generate(body: WarpGenerateBody): Promise<WarpPlan> {
    return call('/warp/generate', body);
  }

  function addToPool(name: string, body: Partial<WarpGenerateBody>): Promise<WarpPlan> {
    return call('/pools/' + encodeURIComponent(name) + '/warp', body);
  }

  return { state, refresh, generate, addToPool };
}
