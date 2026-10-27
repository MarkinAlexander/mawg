// Чистая логика диалогов пулов - порт легаси panel.js (slotBusyHint 730,
// loadSlots 743, gateSlot 429, renderSourcePlan 520) для юнит-тестов.
import type { LinkInspect, SlotInfo, SourcePlan } from '../../api/types'

// слот занят чем-то (легаси: busy = linkUp || connected || description)
export function slotBusy(s: SlotInfo): boolean {
  return !!(s.linkUp || s.connected || s.description);
}

// опции селекта слотов: занятые пулами mawg - прячутся, пометка занят/свободен
export function slotOptions(all: SlotInfo[], mawgSlots: Set<string>): {
  options: { value: string; label: string; busy: boolean }[]
  firstFree: string
  firstAny: string
} {
  const options: { value: string; label: string; busy: boolean }[] = [];
  let firstFree = '', firstAny = '';
  for (const s of all) {
    if (mawgSlots.has(s.id)) continue;
    if (!firstAny) firstAny = s.id;
    const busy = slotBusy(s);
    if (!busy && !firstFree) firstFree = s.id;
    const mark = busy ? ' - занят' : ' - свободен';
    options.push({
      value: s.id,
      label: `${s.id} (${s.device})${s.description ? ' ' + s.description : ''}${mark}`,
      busy,
    });
  }
  return { options, firstFree, firstAny };
}

// подсказка под слотом (легаси gateSlot/slotBusyHint): офф = слот не нужен
export function slotGate(
  mode: 'cfg' | 'link' | 'free',
  inspect: LinkInspect | null | undefined,
  busyPicked?: boolean,
): { off: boolean; text: string } {
  if (mode === 'free') {
    return { off: true, text: 'Пул Amnezia Free идёт через ядро sing-box (tun-интерфейс) - слот WireGuard не нужен.' };
  }
  if (mode === 'cfg') {
    return busyPicked
      ? { off: false, text: 'Слот занят: при активации пула его текущая конфигурация и пиры этого интерфейса будут заменены.' }
      : { off: false, text: '' };
  }
  if (inspect && inspect.amnezia) {
    return { off: false, text: 'Протокол из ключа: AWG - нужен свободный слот; VLESS - слот не нужен (будет tun-интерфейс движка). Оставьте выбранным свободный слот, если протокол AWG.' };
  }
  if (!inspect || inspect.error) {
    return { off: true, text: inspect?.error ? `Ссылка не разбирается: ${inspect.error}` : 'Вставьте ссылку - mawg определит, нужен слот (WG/AWG) или будет создан tun-интерфейс.' };
  }
  if (inspect.native > 0) {
    return {
      off: false,
      text: `Есть WG/AWG узлы (${inspect.native}) - выберите свободный слот или создайте новый. `
        + (inspect.engine > 0 ? `Прокси-узлы (${inspect.engine}) пойдут в tun-интерфейс движка.` : ''),
    };
  }
  return { off: true, text: `Прокси-узлы (${inspect.engine}) - будет создан tun-интерфейс движка sing-box (tun1, tun2...), слот не нужен.` };
}

// нужен ли вопрос «Протокол (OpenWrt)» и что предвыбрать: ссылка парсится
// СРАЗУ и флоу ведёт по результату - движковые узлы (vless/trojan) идут в
// tun движка без вопроса о протоколе, селект WG/AWG - только настоящим
// WG/AWG-узлам и ручным конфигам (слово владельца 2026-10-17)
export function protoGate(
  mode: 'cfg' | 'link' | 'free',
  inspect: LinkInspect | null | undefined,
): { show: boolean; proto: 'wireguard' | 'amneziawg' | '' } {
  if (mode === 'cfg') return { show: true, proto: '' };
  if (mode === 'free') return { show: false, proto: '' };
  if (!inspect || inspect.error) return { show: false, proto: '' };
  if (inspect.amnezia) {
    // ключ Amnezia: VLESS-конфиг идёт движком (вопроса нет), AWG - нативно
    return { show: inspect.amnezia.serviceProtocol !== 'vless', proto: '' };
  }
  if (inspect.native > 0) {
    const types = inspect.types || [];
    return {
      show: true,
      proto: types.includes('amneziawg') && !types.includes('wireguard') ? 'amneziawg' : 'wireguard',
    };
  }
  return { show: false, proto: '' };
}

// текст строки разбора ссылки (легаси inspectSource 498-505)
export function inspectLine(ins: LinkInspect): string {
  const t: string[] = [];
  if (ins.error) t.push('не разбирается: ' + ins.error);
  if (ins.amnezia) t.push(`ключ Amnezia ${(ins.amnezia.serviceType || '').toUpperCase()} API`);
  if (ins.native) t.push(`WG/AWG узлов: ${ins.native}`);
  if (ins.engine) t.push(`прокси-узлов: ${ins.engine} (${(ins.types || []).join(', ')})`);
  if (ins.intervalHours) t.push(`интервал подписки: ${ins.intervalHours} ч`);
  if (!ins.error && !ins.native && !ins.engine && !ins.amnezia) t.push('узлов не найдено');
  return t.join(' · ');
}

// структурированный план создания (легаси renderSourcePlan 520) - рендер
// во вьюхе шаблоном, без v-html
export function planView(plan: SourcePlan): {
  created: '' | 'tun' | 'premium' | 'cfg' | 'none'
  summary: string
  amneziaMeta: string
  engineSavedForLx: boolean
} {
  const warn = plan.warnings && plan.warnings.length;
  let created: '' | 'tun' | 'premium' | 'cfg' | 'none' = '';
  let summary = '';
  if (plan.pool && plan.tun) {
    created = 'tun';
    const applied = plan.applied != null ? plan.applied : ((plan.engine && plan.engine.length) || 0);
    const probe = plan.probeOk == null ? '' : (plan.probeOk ? ` · проба: ok (${plan.probeMs}ms)` : ' · проба: не прошла');
    summary = warn
      ? `Пул ${plan.pool} создан: tun ${plan.tun}, узлов ${applied}${probe}. ${plan.warnings!.join('; ')}`
      : `Пул ${plan.pool} создан: tun-интерфейс ${plan.tun}, узлов ${applied}${probe}.`;
  } else if (plan.pool && plan.premium) {
    created = 'premium';
    summary = `Создан пул ${plan.pool} Amnezia Premium. В его карточке выберите страну и нажмите «Сменить страну». Конфиг пока не запрошен; новый слот устройства не создан.`;
  } else if (plan.pool) {
    created = 'cfg';
    summary = `Пул ${plan.pool} создан: конфигов ${plan.added || 0}`
      + (plan.duplicates && plan.duplicates.length ? `, дубликаты пропущены: ${plan.duplicates.join(', ')}` : '') + '.';
  } else if (plan.engine && plan.engine.length) {
    created = 'none';
    summary = 'Пул не создан: в источнике нет WG/AWG узлов, нативно добавить нечего.';
  }
  let amneziaMeta = '';
  if (plan.amnezia) {
    const a = plan.amnezia;
    amneziaMeta = `Выдан конфиг Amnezia (${(a.protocol || 'awg').toUpperCase()})`;
    if (a.serverCountry) amneziaMeta += `, локация: ${a.serverCountryName || a.serverCountry}`;
    if (a.maxDevices) amneziaMeta += `, устройств: ${a.activeDevices}/${a.maxDevices}`;
    if (a.issuedConfigs) amneziaMeta += `, выдано конфигов: ${a.issuedConfigs}`;
  }
  return { created, summary, amneziaMeta, engineSavedForLx: !!(plan.pool && plan.tun) };
}

// подсказка fallback в настройках (легаси updateFbHint 2018)
export function fallbackHint(
  value: string,
  ifaces: { device: string; probe?: unknown }[] | null,
  ifaceLabel: (dev: string) => string,
): string {
  if (!value.startsWith('iface:')) return '';
  const dev = value.slice('iface:'.length);
  const info = (ifaces || []).find((i) => i.device === dev);
  return info && info.probe
    ? `Группы будут переведены на ${ifaceLabel(dev)} при провале всех конфигов и вернутся, когда пул оживет.`
    : `Внимание: у интерфейса ${ifaceLabel(dev)} нет проверки здоровья. Задайте её в разделе Интерфейсы роутера, иначе момент отката и возврата будет определяться неточно.`;
}

// --- Генератор WARP (77-док) ---

// Сети датацентров WARP: anycast-блоки LxBox (warp_endpoints.json) +
// /24-префиксы warp-generator. Сверено с internal/warp/endpoints.go -
// при изменении там менять и тут.
const WARP_V4_PREFIXES = [
  '162.159.192.', '162.159.193.', '162.159.195.',
  '188.114.96.', '188.114.97.', '188.114.98.', '188.114.99.',
  '8.6.112.', '8.34.70.', '8.34.146.', '8.35.211.',
  '8.39.125.', '8.39.204.', '8.39.214.', '8.47.69.',
];

// hostOf - адресная часть "host:port" (IPv6 в скобках не режется).
export function hostOf(endpoint: string): string {
  if (endpoint.startsWith('[')) {
    const i = endpoint.indexOf(']');
    return i > 0 ? endpoint.slice(1, i) : endpoint;
  }
  const c = endpoint.lastIndexOf(':');
  return c > 0 ? endpoint.slice(0, c) : endpoint;
}

// isWarpEndpoint - эндпоинт указывает на датацентры Cloudflare WARP.
export function isWarpEndpoint(endpoint?: string): boolean {
  if (!endpoint) return false;
  const h = hostOf(endpoint).toLowerCase();
  if (h === 'engage.cloudflareclient.com') return true;
  if (h.startsWith('2606:4700:d0:') || h.startsWith('2606:4700:d1:')) return true;
  return WARP_V4_PREFIXES.some((p) => h.startsWith(p));
}

// isWarpPool - пул варповый: создан генератором (мета) или хотя бы один
// конфиг смотрит в сети Cloudflare (владелец: узнать и чужой пул).
export function isWarpPool(p: { settings?: { warp?: unknown }; configs?: { endpoint?: string }[] }): boolean {
  if (p.settings?.warp) return true;
  return (p.configs || []).some((c) => isWarpEndpoint(c.endpoint));
}

// agoText - сколько времени назад была проба (probeAt - unix-сек из
// /status и /ifaces: время той же единственной пробы пула).
export function agoText(at?: number, now = Date.now()): string {
  if (!at) return '';
  const s = Math.max(0, Math.round((now - at * 1000) / 1000));
  if (s < 10) return 'только что';
  if (s < 60) return s + ' с назад';
  const m = Math.floor(s / 60);
  if (m < 60) return m + ' мин назад';
  const h = Math.floor(m / 60);
  if (h < 48) return h + ' ч назад';
  return Math.floor(h / 24) + ' дн назад';
}
