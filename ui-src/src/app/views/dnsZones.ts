// Чистые функции домена «зонные DNS» (форк rev13): нормализация имён,
// валидация формы, сборка записи, опции пула и тексты подтверждений.
import type { UiSelectOption } from '../../components/ui';
import type { DnsProbeRow, DnsUpstream, DnsZone, DnsZonesRes } from '../../api/types';
import { linesOf } from './dnsLogic';
import { addrErr } from './dnsServers';

// форма зоны в диалоге: idx -1 = новая, иначе правка записи списка
export interface ZoneForm {
  idx: number;
  zone: string;
  servers: string;
  pool: string;
  fallbackOpen: boolean;
  enable: boolean;
}

// имя зоны: нижний регистр без хвостовой точки
export const normZone = (name: string): string => name.trim().toLowerCase().replace(/\.+$/, '');

// валидация формы зоны: ошибка - текстом, ок - список серверов
export function zoneValidateOf(f: ZoneForm, zones: DnsZone[]): { err: string; servers: string[] | null } {
  const zone = normZone(f.zone);
  if (!zone) return { err: 'укажите имя зоны', servers: null };
  if (/[ \t/\\]|:\/\//.test(zone) || zone.includes('..')) return { err: 'кривое имя зоны', servers: null };
  if (zones.some((z, i) => i !== f.idx && z.zone === zone)) return { err: 'такая зона уже есть в списке', servers: null };
  const servers = linesOf(f.servers);
  if (!servers.length) return { err: 'укажите хотя бы один DNS-сервер зоны', servers: null };
  for (const a of servers) {
    if (addrErr(a) !== null && !a.startsWith('tls://') && !a.startsWith('https://')) {
      return { err: `кривой адрес: ${a}`, servers: null };
    }
  }
  return { err: '', servers };
}

export function zoneLiveMapOf(res: DnsZonesRes | null): Record<string, { fallback: boolean; upstreams: DnsUpstream[] }> {
  const m: Record<string, { fallback: boolean; upstreams: DnsUpstream[] }> = {};
  for (const z of res?.live?.zones || []) m[z.zone] = z;
  return m;
}

// запись таблицы зон из формы: правка по idx, новая - с новым идом
export function zoneOf(f: ZoneForm, zones: DnsZone[], servers: string[], nowMs: number): DnsZone {
  return {
    id: f.idx >= 0 ? zones[f.idx].id : 'z' + nowMs.toString(16),
    zone: normZone(f.zone),
    upstreams: servers,
    pool: f.pool || undefined,
    fallbackOpen: f.fallbackOpen,
    enable: f.enable,
  };
}

export function poolOptionsOf(pools: { name: string }[], ifaces: { device: string; title?: string }[]): UiSelectOption[] {
  // дедуп (скрин друга): интерфейс, чьё устройство ИЛИ подпись - это имя
  // пула, и есть тот же туннель (OpenWrt: device = имя пула; движок:
  // tunN с title = пул; Кинетик: nwgN с title = пул) - пул уже в списке
  const names = new Set(pools.map((p) => p.name.toLowerCase()));
  const uniq = ifaces.filter((i) => {
    if (names.has(i.device.toLowerCase())) return false;
    const title = (i.title || '').toLowerCase();
    return !names.has(title);
  });
  return [
    { value: '', label: 'напрямую (без туннеля)' },
    ...pools.map((p) => ({ value: p.name, label: p.name })),
    ...uniq.map((i) => ({
      value: 'iface:' + i.device,
      label: i.device + (i.title && i.title !== i.device ? ' - ' + i.title : ''),
    })),
  ];
}

export function zoneSaveAskOf(zone: DnsZone, servers: string[]): { title: string; text: string; ok: string } {
  return {
    title: 'Зонные DNS',
    text: `Запросы имён «${zone.zone}» и всех поддоменов уйдут к ${servers[0]}`
      + (zone.pool ? ` через туннель «${zone.pool}»` : ' напрямую')
      + (zone.fallbackOpen ? '. При полном отвале серверов зоны - общий список DNS.' : '. При полном отвале серверов зона будет недоступна (запросы не уйдут в общий DNS).'),
    ok: 'Применить',
  };
}

export function zoneRemoveAskOf(z: DnsZone): { title: string; text: string; okLabel: string } {
  return { title: 'Убрать зону', text: `Зона «${z.zone}» выйдет из зонной таблицы: её имена будет резолвить общий список DNS.`, okLabel: 'Убрать' };
}

export function zoneToggleAskOf(z: DnsZone): { title: string; text: string; okLabel: string } {
  const toOn = !z.enable;
  return {
    title: toOn ? 'Включить зону' : 'Выключить зону',
    text: toOn
      ? `Запросы «${z.zone}» уйдут к своим серверам зоны${z.pool ? ` через туннель «${z.pool}»` : ''}.`
      : `Зона «${z.zone}» перестанет перехватываться: её имена будет резолвить общий список DNS.`,
    okLabel: toOn ? 'Включить' : 'Выключить',
  };
}

// последняя проба зоны прошла: хотя бы один сервер чисто ответил
// через путь клиентов
export function zoneProbeOkOf(rows: DnsProbeRow[] | undefined): boolean {
  return !!rows && rows.length > 0 && rows.some((r) => r.verdict === 'clean');
}

export function a2key(z: DnsZone, i: number): string {
  return z.id + ':' + i;
}
