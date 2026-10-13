// Мутации пулов (легаси actPool 367/upload 352/toggleCfg 377/moveCfg 388/
// delCfg 393/delPool 398/importPremium/premium-country). Данные карточек -
// из useStatus() (GET /status каждые 5с); после мутаций - отложенное
// перечитывание (легаси setTimeout(refresh, 700)), кроме быстрых конфиг-хвостов
// - там перечитывание сразу.
import { api, apiForm } from '../api/client'
import { useStatus } from './status'

export type PoolAct = 'enable' | 'disable' | 'rotate' | 'check';

// ответ POST /pools/{name}/configs (легаси upload)
export interface UploadRes {
  added: number
  duplicates?: string[]
  errors?: string[]
  warnings?: string[]
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

  return { act, del, upload, toggleCfg, moveCfg, delCfg, importPremium, switchPremiumCountry };
}
