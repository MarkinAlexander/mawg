// Чистая логика диалога настроек пула: сборка тел запросов, дефолты
// полей, тексты и опции селектов. Вынесено из PoolSettingsDialog.vue -
// refs/computed и мутации остаются во вьюхе, сюда передаются параметрами.
import type { UiSelectOption } from '../../components/ui'
import type { IfaceInfo, PoolMember, PoolSettings, PoolView, SmartCascadeSettings, SourcePlan } from '../../api/types'
import { addrSecretIn, hostOf } from '../secrets'

// имя участника скрывается, если в нём сидит хост его же endpoint или
// просто IPv4 (слово владельца 2026-10-17: тег узла выдаёт адрес)
export function nameSecret(m: PoolMember): boolean {
  if (!m.name) return false;
  if (addrSecretIn(m.name)) return true;
  return !!m.endpoint && m.name.includes(hostOf(m.endpoint));
}

export const probeTypeOptions: UiSelectOption[] = [
  { value: 'http', label: 'HTTP-страница 204 (надежнее, ICMP режут)' },
  { value: 'icmp', label: 'Ping публичного IP' },
];
export const groupModeOptions: UiSelectOption[] = [
  { value: 'urltest', label: 'лучший пинг (авто)' },
  { value: 'selector', label: 'ручной выбор узла' },
  { value: 'rotate', label: 'ротация по таймеру (только рабочие)' },
];
export const coreOptions: UiSelectOption[] = [
  { value: '', label: 'sing-box' },
  { value: 'mihomo', label: 'mihomo (Clash.Meta)' },
];
export const scModeOptions: UiSelectOption[] = [
  { value: 'schedule', label: 'по расписанию (в определённое время)' },
  { value: 'interval', label: 'по интервалу (раз в N часов)' },
  { value: 'manual', label: 'только кнопкой' },
];
export const scTrigOptions: UiSelectOption[] = [
  { value: 'exhaust', label: 'после перебора всех конфигов пула' },
  { value: 'fails', label: 'после отказов подряд (быстрее)' },
];

// короткое имя узла движка: тег вида «подписка|узел» - после «|»
export function nodeShortName(tag: string): string {
  return tag.includes('|') ? tag.slice(tag.indexOf('|') + 1) : tag;
}

export function nodeOptionsOf(nodes: { tag: string; delay: number; off?: boolean }[]): UiSelectOption[] {
  return nodes
    .filter((n) => !n.off)
    .map((n) => {
      const nm = nodeShortName(n.tag);
      return { value: n.tag, label: `${nm}${n.delay > 0 ? ` - ${n.delay} мс` : ''}` };
    });
}

export function amnCountryOptionsOf(countries: { code: string; name?: string; protocols?: string[] }[] | undefined): UiSelectOption[] {
  return (countries || [])
    .filter((c) => (c.protocols ? c.protocols.length > 0 : true))
    .map((c) => ({
      value: c.code,
      label: c.code + (c.name ? ' - ' + c.name : '') + (c.protocols && c.protocols.length ? ` [${c.protocols.join('/')}]` : ''),
    }));
}

export function showAmnCountryOf(countries: { code: string }[] | undefined): boolean {
  return (countries || []).length >= 2;
}

export function premCountryOptionsOf(countries: { code: string; name: string }[]): UiSelectOption[] {
  return countries.map((c) => ({ value: c.code, label: `${c.name} (${c.code})` }));
}

export function premNoteOf(pending: boolean | undefined): string {
  return pending
    ? 'Смена не завершена: регистрация у gateway могла измениться - повторите выбор страны.'
    : 'Один слот устройства. Страны не добавляются в пул конфигов.';
}

// опции фоллбека: напрямую/держать + другие пулы + внешние интерфейсы
export function fbOptionsOf(pools: PoolView[], ifaces: IfaceInfo[], poolName: string, device?: string): UiSelectOption[] {
  const opts: UiSelectOption[] = [
    { value: 'direct', label: 'пустить трафик напрямую' },
    { value: 'hold', label: 'держать последний конфиг' },
  ];
  for (const other of pools) {
    if (other.name !== poolName) {
      opts.push({ value: `pool:${other.name}`, label: `перевести группы на пул ${other.name}` });
    }
  }
  for (const i of ifaces) {
    if (i.device === device || i.mode === 'managed') continue;
    opts.push({ value: `iface:${i.device}`, label: `перевести группы на интерфейс ${i.device}${i.probe ? '' : ' (без проверки)'}` });
  }
  return opts;
}

// запасные для каскада: прочие включённые пулы
export function scPoolOptionsOf(pools: PoolView[], poolName: string): UiSelectOption[] {
  return pools
    .filter((p) => p.name !== poolName && !p.disabled)
    .map((p) => ({ value: p.name, label: p.name }));
}

// дефолты пробы при открытии: движок всегда через HTTP-страницу, Free -
// адрес из списка сервиса, прочие - публичный generate_204
export function probeTypeFor(settings: PoolSettings, isEng: boolean): string {
  if (isEng) return 'http';
  return /^https?:\/\//.test(settings.probeHost || '') ? 'http' : 'icmp';
}

export function probeHostFor(settings: PoolSettings, isFree: boolean): string {
  return settings.probeHost || (isFree ? 'http://1.1.1.1/' : 'http://www.gstatic.com/generate_204');
}

// IP или подсеть для привязки устройства (97-док)
export const DEV_IP_RE = /^\d{1,3}(\.\d{1,3}){3}(\/\d{1,2})?$/;

// разбор черновика адресов: пачка через разделители -> список
export function splitIps(text: string): string[] {
  return text.split(/[\s,;]+/).map((x) => x.trim()).filter(Boolean);
}

// черновик умного каскада (86-док) из настроек пула: строки для полей
export interface ScDraft {
  on: boolean
  via: string[]
  mode: string
  trigMode: string
  trigFails: string
  interval: string
  times: string
  dwell: string
}

export function scDraftOf(sc?: SmartCascadeSettings): ScDraft {
  const scRet = sc?.return || {};
  return {
    on: !!(sc?.via && sc.via.length),
    via: [...(sc?.via || [])], // порядок = приоритет, храним как есть
    mode: scRet.mode === 'interval' || scRet.mode === 'manual' ? scRet.mode : 'schedule',
    trigMode: sc?.triggerMode === 'fails' ? 'fails' : 'exhaust',
    trigFails: String((sc?.triggerFails || 0) > 0 ? sc!.triggerFails! : 3),
    interval: String((scRet.intervalH || 0) > 0 ? (scRet.intervalH as number) : 6),
    times: (scRet.timeOfDay && scRet.timeOfDay.length ? scRet.timeOfDay : ['03:00']).join(', '),
    dwell: String((sc?.minDwellMin || 0) > 0 ? sc!.minDwellMin! : 10),
  };
}

// умный каскад: полный объект при включении, пустой via = выключить
export function smartCascadeBody(draft: ScDraft, knownPools: Set<string>): Record<string, unknown> {
  const via = draft.via.filter((v) => knownPools.has(v)); // порядок списка = приоритет
  return draft.on && via.length
    ? {
      smartCascade: {
        via,
        triggerMode: draft.trigMode,
        ...(draft.trigMode === 'fails' ? { triggerFails: Math.max(1, +draft.trigFails || 3) } : {}),
        return: {
          mode: draft.mode,
          ...(draft.mode === 'interval' ? { intervalH: Math.max(1, +draft.interval || 6) } : {}),
          ...(draft.mode === 'schedule' ? { timeOfDay: draft.times.split(/[,\s]+/).map((t) => t.trim()).filter(Boolean) } : {}),
        },
        minDwellMin: Math.max(0, +draft.dwell || 0),
      },
    }
    : { smartCascade: { via: [] } };
}

// итог «обновить из источника»: что сделали и с чем не согласились
export function refreshSourceMsg(plan: SourcePlan): string {
  let msg = 'источник обновлён';
  if (plan.amnezia) msg = 'ключ Amnezia обменян' + (plan.amnezia.serverCountry ? ` (локация ${plan.amnezia.serverCountry})` : '');
  if (plan.added) msg += `: конфигов ${plan.added}`;
  if (plan.engine && plan.engine.length) msg += `, узлов ${plan.engine.length}`;
  if (plan.warnings && plan.warnings.length) msg += '; ' + plan.warnings.join('; ');
  return msg;
}
