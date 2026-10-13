// Мутации пулов (легаси actPool 367/upload 352/toggleCfg 377/moveCfg 388/
// delCfg 393/delPool 398/importPremium/premium-country, создание 670-805,
// настройки openSettings/spSave 935-1043). Данные карточек - из useStatus()
// (GET /status каждые 5с); после мутаций - отложенное перечитывание
// (легаси setTimeout(refresh, 700)), кроме быстрых конфиг-хвостов - там сразу.
import { api, apiForm, apiText } from '../api/client'
import { useStatus } from './status'
import type { LinkInspect, PoolMember, SlotInfo } from '../api/types'

export type PoolAct = 'enable' | 'disable' | 'rotate' | 'check';

// ответ POST /pools/{name}/configs (легаси upload)
export interface UploadRes {
  added: number
  duplicates?: string[]
  errors?: string[]
  warnings?: string[]
}

// тело создания пула из конфигов (легаси pCreate, режим cfg)
export interface CreatePoolBody {
  name: string
  fallback: string
  keeneticSlot?: string
  openwrtProto?: string
}

export function usePoolActions() {
  const { refresh } = useStatus();

  function later(): void {
    setTimeout(() => void refresh(), 700);
  }

  async function act(name: string, act: PoolAct): Promise<void> {
    await api('POST', `/pools/${encodeURIComponent(name)}/${act}`, {});
    later();
  }

  async function del(name: string): Promise<void> {
    await api('DELETE', `/pools/${encodeURIComponent(name)}`);
    void refresh();
  }

  async function upload(name: string, files: FileList | File[]): Promise<UploadRes> {
    const list = Array.from(files);
    if (!list.length) return { added: 0 };
    const fd = new FormData();
    for (const f of list) fd.append('files', f);
    const r = await apiForm<UploadRes>('POST', `/pools/${encodeURIComponent(name)}/configs`, fd);
    void refresh();
    return r;
  }

  // сервер отказал (например, адрес занят другим интерфейсом) - стор просто
  // перечитывает статус, тумблер возвращается к фактическому состоянию
  async function toggleCfg(name: string, file: string, enabled: boolean): Promise<void> {
    await api('POST', `/pools/${encodeURIComponent(name)}/configs/${encodeURIComponent(file)}/enable`, { enabled });
    void refresh();
  }

  async function moveCfg(name: string, file: string, delta: number): Promise<void> {
    await api('POST', `/pools/${encodeURIComponent(name)}/configs/${encodeURIComponent(file)}/move`, { delta });
    void refresh();
  }

  async function delCfg(name: string, file: string): Promise<void> {
    await api('DELETE', `/pools/${encodeURIComponent(name)}/configs/${encodeURIComponent(file)}`);
    void refresh();
  }

  async function importPremium(name: string, key: string): Promise<void> {
    await api('POST', `/pools/${encodeURIComponent(name)}/premium`, { key });
    void refresh();
  }

  async function switchPremiumCountry(name: string, country: string): Promise<void> {
    await api('POST', `/pools/${encodeURIComponent(name)}/premium/country`, { country });
    void refresh();
  }

  // --- Ф3.5б: создание/настройки ---

  async function create(body: CreatePoolBody): Promise<void> {
    await api('POST', '/pools', body);
    void refresh();
  }

  async function createFromSource(body: CreatePoolBody & { source: string; exchange?: boolean; via?: string; country?: string }): Promise<SourcePlanRes> {
    const r = await api<SourcePlanRes>('POST', '/pools/from-source', body);
    void refresh();
    return r;
  }

  // Amnezia Free: повтор с решением капчи - тем же телом + captchaId/Solution
  async function requestFree(fields: { name: string; fallback: string; keeneticSlot?: string }, answer?: { id: string; solution: string }): Promise<SourcePlanRes> {
    const body = answer ? { ...fields, captchaId: answer.id, captchaSolution: answer.solution } : fields;
    const r = await api<SourcePlanRes>('POST', answer ? '/pools/amnezia-free/captcha' : '/pools/amnezia-free', body);
    void refresh();
    return r;
  }

  async function inspect(source: string): Promise<LinkInspect> {
    return api<LinkInspect>('POST', '/links/inspect', { source });
  }

  async function slots(): Promise<SlotInfo[]> {
    const r = await api<{ slots: SlotInfo[] }>('GET', '/slots');
    return r.slots || [];
  }

  async function createSlot(): Promise<string> {
    const r = await api<{ slot: string }>('POST', '/slots/create', {});
    return r.slot || '';
  }

  async function refreshSource(name: string, body: { source: string; country?: string }): Promise<SourcePlanRes> {
    const r = await api<SourcePlanRes>('POST', `/pools/${encodeURIComponent(name)}/refresh-source`, body);
    void refresh();
    return r;
  }

  async function update(name: string, body: Record<string, unknown>): Promise<void> {
    await api('PUT', `/pools/${encodeURIComponent(name)}`, body);
    void refresh();
  }

  async function rename(name: string, newName: string): Promise<void> {
    await api('POST', `/pools/${encodeURIComponent(name)}/rename`, { name: newName });
    later();
  }

  async function reissueFree(name: string, answer?: { id: string; solution: string }): Promise<SourcePlanRes> {
    const r = await api<SourcePlanRes>('POST', `/pools/${encodeURIComponent(name)}/free/reissue`, answer ? { captchaId: answer.id, captchaSolution: answer.solution } : {});
    void refresh();
    return r;
  }

  async function members(name: string): Promise<PoolMember[]> {
    const r = await api<{ members: PoolMember[] }>('GET', `/pools/${encodeURIComponent(name)}/members`);
    return r.members || [];
  }

  async function configText(name: string, file: string): Promise<string> {
    return apiText(`/pools/${encodeURIComponent(name)}/configs/${encodeURIComponent(file)}`);
  }

  return {
    act, del, upload, toggleCfg, moveCfg, delCfg, importPremium, switchPremiumCountry,
    create, createFromSource, requestFree, inspect, slots, createSlot,
    refreshSource, update, rename, reissueFree, members, configText,
  };
}

// SourcePlan из api/types (план создания/обновления из источника)
type SourcePlanRes = import('../api/types').SourcePlan;
