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
