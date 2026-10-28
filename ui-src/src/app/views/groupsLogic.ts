// Чистая логика карточки «Правила маршрутизации» (Ф3.7а) - порт констант
// и функций легаси panel.js для юнит-тестов без DOM.
// Референсы: RULE_TYPES 1248, RULE_HINTS 1249, RULE_PLACEHOLDERS 1256,
// POLICY_HINTS 1359, POLICY_SHORT 1614, DEGRADED_TXT 1615, hlText 1570,
// ruleMatches 1577, groupMatches 1580, groupMatchCount 1584, /32 1349-1717,
// warnTitle 1494, srcBadge 1628, showMTDupsDlg 1752, polCyclesValid 1213.
import type { GroupView, Rule } from '../../api/types'

export const RULE_TYPES: Record<string, string> = {
  namespace: 'домен+поддомены',
  domain: 'домен',
  subnet: 'подсеть',
  wildcard: 'маска (* ?)',
  regex: 'регулярка',
};

export const RULE_HINTS: Record<string, string> = {
  domain: 'Точный домен без поддоменов: sub.example.com покроет только его самого',
  namespace: 'Домен и все поддомены: example.com покроет также sub.example.com',
  subnet: 'Подсеть IPv4: 149.154.160.0/20',
  wildcard: 'Шаблон: * любое число символов, ? ровно один. Пример: *example.com',
  regex: 'Регулярное выражение regexp2. Пример: ^[a-z]*example\\.com$',
};

export const RULE_PLACEHOLDERS: Record<string, string> = {
  domain: 'sub.example.com',
  namespace: 'example.com',
  subnet: '149.154.160.0/20',
  wildcard: '*example.com',
  regex: '^[a-z]*example\\.com$',
};

// селекты кита (порядок легаси-словаря значим: namespace первым)
export function ruleTypeOptions(exclude: string[] = []): { value: string; label: string }[] {
  return Object.entries(RULE_TYPES)
    .filter(([v]) => !exclude.includes(v))
    .map(([value, label]) => ({ value, label }));
}

// типы импорта текстом (легаси imType: + auto, порядок другой)
export const IMPORT_TYPE_OPTIONS: { value: string; label: string }[] = [
  { value: 'auto', label: 'авто (IP и CIDR как подсети, остальное как домен+поддомены)' },
  { value: 'namespace', label: 'домен+поддомены' },
  { value: 'domain', label: 'домен (без поддоменов)' },
  { value: 'wildcard', label: 'маска (* ?)' },
  { value: 'subnet', label: 'подсеть' },
  { value: 'regex', label: 'регулярка (по одной на строку)' },
];

export const POLICY_HINTS: Record<string, string> = {
  '': 'При отвале интерфейса группа ведет себя как обычно: на пуле выключится при его фоллбеке.',
  direct: 'Если основной интерфейс умрет, трафик группы пойдет напрямую (проверив, что есть интернет) и вернется сам.',
  iface: 'Если основной интерфейс умрет, группа перейдет на запасной интерфейс и вернется сам.',
};

export const POLICY_OPTIONS: { value: string; label: string }[] = [
  { value: '', label: 'по умолчанию (как у пула)' },
  { value: 'direct', label: 'идти напрямую (проверив интернет)' },
  { value: 'iface', label: 'переключить на запасной интерфейс' },
];

export const POLICY_SHORT: Record<string, string> = {
  direct: 'напрямую',
  iface: 'запасной интерфейс',
};

export const DEGRADED_TXT: Record<string, string> = {
  direct: 'идет напрямую: основной интерфейс умер, вернется сам',
  'direct-pending': 'ждет возврата интернета, потом пойдет напрямую',
  iface: 'временно переключена на запасной интерфейс',
};

export function degradedText(g: GroupView): string {
  if (!g.degraded) return '';
  return DEGRADED_TXT[g.degraded.split(':')[0]] || 'состояние: ' + g.degraded;
}

// --- поиск ---

export interface HlSeg {
  text: string
  hit: boolean
}

// подсветка поиска (легаси hlText 1570): сегменты до/совпадение/после,
// <mark> ставит шаблон - HTML не ломается
export function hlSegs(raw: unknown, q: string): HlSeg[] {
  const s = String(raw ?? '');
  if (!q) return [{ text: s, hit: false }];
  const i = s.toLowerCase().indexOf(q);
  if (i < 0) return [{ text: s, hit: false }];
  return [
    { text: s.slice(0, i), hit: false },
    { text: s.slice(i, i + q.length), hit: true },
    { text: s.slice(i + q.length), hit: false },
  ].filter((x) => x.text);
}

export function ruleMatches(rl: Rule, q: string): boolean {
  return (rl.rule || '').toLowerCase().includes(q) || (rl.name || '').toLowerCase().includes(q);
}

export function groupMatches(g: GroupView, q: string): boolean {
  if (!q) return false;
  return (g.name || '').toLowerCase().includes(q) || (g.rules || []).some((rl) => ruleMatches(rl, q));
}

export function groupMatchCount(g: GroupView, q: string): number {
  if (!q) return 0;
  let n = (g.name || '').toLowerCase().includes(q) ? 1 : 0;
  for (const rl of g.rules || []) if (ruleMatches(rl, q)) n++;
  return n;
}

// при поиске внутри группы показываются только совпавшие правила, кроме
// случая «совпало имя, но ни одно правило» - тогда группа целиком
// (легаси 1680-1684)
export function showAllRules(g: GroupView, q: string): boolean {
  if (!q) return true;
  const nameMatched = (g.name || '').toLowerCase().includes(q);
  const hasRuleMatches = (g.rules || []).some((rl) => ruleMatches(rl, q));
  return nameMatched && !hasRuleMatches;
}

// чекер скрытия выключенного (80-док, бонус; слово владельца: два
// РАЗДЕЛЬНЫХ тумблера - можно скрыть неактивные группы, а правила
// оставить видимыми, чтобы их включать): фильтры отображения выключенных
// групп и выключенных правил. Чистые функции - юнит-тесты без DOM;
// composing с поиском - на стороне вьюхи (сначала поиск, потом скрытие).
export function hideOffGroups(groups: GroupView[], hide: boolean): GroupView[] {
  return hide ? groups.filter((g) => g.enable) : groups;
}

export function hideOffRules(rules: Rule[], hide: boolean): Rule[] {
  return hide ? rules.filter((rl) => rl.enable) : rules;
}

// счётчики для тумблеров («выключено: N» рядом с каждым)
export function offGroupsTotal(groups: GroupView[]): number {
  return groups.reduce((n, g) => (g.enable ? n : n + 1), 0);
}

export function offRulesTotal(groups: GroupView[]): number {
  return groups.reduce((n, g) => n + (g.rules || []).filter((rl) => !rl.enable).length, 0);
}

// subnet без маски получает /32 (легаси 1349, 1717)
export function normRuleValue(type: string, value: string): string {
  return type === 'subnet' && /^\d{1,3}(\.\d{1,3}){3}$/.test(value) ? value + '/32' : value;
}

// --- бейджи ---

// тултип warn-бейджа дубликата (легаси warnTitle 1494); refs из
// /mt/duplicates - {groupName, ruleType}, из диалога поиска - {group, type}
export interface DupRef {
  groupName?: string
  group?: string
  ruleType?: string
  type?: string
}

export function warnTitle(refs: DupRef[]): string {
  const others = refs.map((r) => `${r.groupName || r.group} (${RULE_TYPES[r.ruleType || r.type || ''] || r.ruleType || r.type})`);
  return 'Такой же паттерн в других группах: ' + others.join(', ') + '. Магитрикл применит только первое правило.';
}

// у группы есть включённые правила-дубликаты (легаси groupWarnSvg 1504)
export function groupHasDup(g: GroupView, duplicates: Record<string, unknown[]>): boolean {
  return (g.rules || []).some((rl) => rl.enable && ((duplicates[(rl.rule || '').toLowerCase()] || []).length > 0));
}

// «кто ходит в группу» (легаси 1628-1637): подписанный чип; для групп без
// политики - фактические sources (созданы вне mawg или импортом)
// группа «устройство целиком через пул» (88+): sources непусты + статика
// 0.0.0.0/0 - весь трафик перечисленных адресов уходит в интерфейс группы
export function isDeviceGroup(g: GroupView): boolean {
  if (!g.sources || !g.sources.length) return false;
  return (g.rules || []).some((r) => r.enable && r.type === 'subnet' && r.rule === '0.0.0.0/0');
}

export function srcBadge(g: GroupView): { text: string; tooltip: string } | null {
  const pol = g.policy || {};
  if (pol.sourcesMode === 'all+router') {
    return { text: 'источники: локалка + роутер', tooltip: 'в группу ходит вся локалка и сам роутер' };
  }
  if (pol.sourcesMode === 'list') {
    const rn = pol.sourcesRouter ? ' + роутер' : '';
    return {
      text: `источники: IP-список (${(pol.sources || []).length})${rn}`,
      tooltip: 'в группу ходит трафик только с этих адресов: ' + (g.sources || []).join(', '),
    };
  }
  if (!pol.sourcesMode && (g.sources || []).length) {
    return {
      text: `источники: IP-список (${(g.sources || []).length})`,
      tooltip: 'в группу ходит трафик только с этих адресов: ' + (g.sources || []).join(', '),
    };
  }
  return null;
}

// --- диалог дубликатов (легаси showMTDupsDlg 1752-1784) ---

export interface DupsEntry {
  pat: string
  types: string
  groups: string[]
  dup: boolean
}

// без поиска - межгрупповые дубликаты из /mt/duplicates; с поиском - ВСЕ
// вхождения найденных паттернов по группам (даже не дубликат: юзеру важно
// увидеть, где правило живёт)
export function dupsEntries(
  groups: GroupView[],
  duplicates: Record<string, DupRef[]>,
  q: string,
): DupsEntry[] {
  const norm = (r: DupRef): { name: string; type: string } => ({
    name: r.groupName || r.group || '',
    type: r.ruleType || r.type || '',
  });
  let entries: [string, DupRef[]][];
  if (q) {
    const map = new Map<string, DupRef[]>();
    for (const g of groups) {
      for (const rl of g.rules || []) {
        const key = (rl.rule || '').trim().toLowerCase();
        if (!key || !key.includes(q)) continue;
        if (!map.has(key)) map.set(key, []);
        map.get(key)!.push({ group: g.name, type: rl.type });
      }
    }
    entries = [...map.entries()];
  } else {
    entries = Object.entries(duplicates || {});
  }
  entries.sort((a, b) => a[0].localeCompare(b[0]));
  return entries.map(([pat, refs]) => {
    const ns = refs.map(norm);
    const types = [...new Set(ns.map((r) => RULE_TYPES[r.type] || r.type))].join(', ');
    const groupNames = [...new Set(ns.map((r) => r.name))];
    return { pat, types, groups: groupNames, dup: groupNames.length > 1 };
  });
}

// --- гистерезис политик (легаси polCyclesValid 1213) ---

export function cyclesValid(fail: string, restore: string): boolean {
  const okF = /^[1-9][0-9]?$/.test(fail) && +fail <= 20;
  const okR = /^[1-9][0-9]?$/.test(restore) && +restore <= 20;
  return okF && okR;
}

// --- источники группы (легаси SRC_MODE_HINTS 1411, заполнение 1379-1389) ---

export const SRC_MODE_HINTS: Record<string, string> = {
  all: 'Любое устройство локальной сети: группа ловит весь трафик к своим доменам. Роутер в группу не ходит.',
  'all+router': 'Как «вся локалка», плюс собственный трафик роутера уходит в группу (проверка IP с роутера покажет адрес туннеля).',
  list: 'В группу ходят только перечисленные адреса (роутер - по галке). Остальная локалка - напрямую.',
};

// человеческий вердикт пробы для тостов (словарь строк rotator.go 601-641;
// жалоба владельца: «icmp lossy» непонятно). Сырая строка сохраняется.
export function probeHuman(s: string | undefined): string {
  if (!s || s === '-') return 'результ ещё нет';
  if (s.includes('lossy')) return 'канал жив, но пинг выше порога (lossy): ' + s;
  if (s.startsWith('ok') || s.includes(': ok')) return 'туннель работает: ' + s;
  if (s.includes('выше порога')) return 'отказ: пинг выше порога: ' + s;
  if (s.includes('link down')) return 'интерфейс опущен: ' + s;
  if (s.includes('settling')) return 'конфиг применён, идёт стабилизация: ' + s;
  if (s.includes('all configs failed')) return 'все конфиги не прошли проверку: ' + s;
  if (s.includes('failed') || s.includes('error')) return 'ошибка проверки: ' + s;
  return 'результат: ' + s;
}

export const SRC_MODE_OPTIONS: { value: string; label: string }[] = [
  { value: 'all', label: 'вся локальная сеть' },
  { value: 'all+router', label: 'локальная сеть + роутер' },
  { value: 'list', label: 'только выбранные IP' },
];

// режим при открытии: из политики, для чужих групп - вывод из фактических
// sources; у каскадов - all (легаси 1381)
export function srcModeInitial(g: GroupView): '' | 'all' | 'all+router' | 'list' {
  const pol = g.policy || {};
  return pol.sourcesMode || (g.cascade ? 'all' : ((g.sources || []).length ? 'list' : 'all'));
}

// список при открытии: политика или фактические sources через запятую (1382)
export function srcListInitial(g: GroupView): string {
  const pol = g.policy || {};
  return (pol.sources && pol.sources.length ? pol.sources : (g.sources || [])).join(', ');
}

// split по запятым/переводам строк (легаси egSave 2133)
export function parseSources(text: string): string[] {
  return text.split(/[,\n]+/).map((s) => s.trim()).filter(Boolean);
}

// --- пресеты (легаси presetApply 2240-2259) ---

export const PRESET_TYPE_LABELS: Record<string, string> = {
  namespace: 'домен+поддомены',
  subnet: 'подсети IPv4',
  subnet6: 'подсети IPv6',
  wildcard: 'маски',
  regex: 'регулярки',
};

// сводка для confirm: «{title} - правил: N (домен+поддомены: X, подсети IPv4: Y)»
export function presetRulesLine(p: { title: string; rules: { type: string }[] } | null, presetId: string): string {
  if (!p) return `Шаблон: ${presetId}`;
  const cnt: Record<string, number> = {};
  for (const rl of p.rules || []) cnt[rl.type] = (cnt[rl.type] || 0) + 1;
  const parts = Object.entries(cnt).map(([t, n]) => `${PRESET_TYPE_LABELS[t] || t}: ${n}`);
  return `${p.title} - правил: ${(p.rules || []).length}${parts.length ? ' (' + parts.join(', ') + ')' : ''}`;
}

// --- каскады (легаси cascadeOptions 2050, fillCascadeSel 2057) ---

export interface CascadeOpt {
  ref: string
  label: string
  disabled?: boolean
}

// варианты: пулы + интерфейсы (без blackhole)
export function cascadeOptions(
  pools: { name: string }[],
  ifaces: { device: string; title?: string }[],
): CascadeOpt[] {
  const out = pools.map((p) => ({ ref: 'pool:' + p.name, label: 'пул ' + p.name }));
  for (const i of ifaces) {
    if (i.device === 'blackhole') continue;
    out.push({ ref: 'iface:' + i.device, label: 'интерфейс ' + (i.title && i.title !== i.device ? i.device + ' - ' + i.title : i.device) });
  }
  return out;
}

// соседний список дизейблит выбранный у соседа: источник ≠ путь; если
// текущее значение попало в дизейбл - первый разрешённый (легаси 2059-2062)
export function wireCascadeSel(opts: CascadeOpt[], otherValue: string, current: string): { opts: CascadeOpt[]; value: string } {
  const list = opts.map((o) => ({ ...o, disabled: o.ref === otherValue }));
  let value = current;
  const cur = list.find((o) => o.ref === value && !o.disabled);
  if (!cur) {
    const first = list.find((o) => !o.disabled);
    value = first ? first.ref : '';
  }
  return { opts: list, value };
}

// --- бэкапы / импорт / выгрузка файлом (легаси 1786-2010) ---

export interface ImportGroupSrc {
  name: string
  color: string
  interface: string
  enable: boolean
  rules: { type: string; rule: string; enable: boolean; name?: string }[]
}

export interface ImportFileRes {
  error?: string
  name?: string
  groups?: ImportGroupSrc[]
  bad?: number
}

// разбор файла импорта (легаси mtImportPick 1841-1877): mt-config-*
// отвергается с подсказкой про «восстановить», JSON с groups, пригодность =
// имя+интерфейс, правила с пустым паттерном выкидываются в счётчик bad
export function parseImportFile(fileName: string, text: string): ImportFileRes {
  if (/^mt-config-/.test(fileName)) {
    return { error: 'Это сырой MT-конфиг (config.yaml), группы из него не импортируются - восстановите его кнопкой «восстановить» в списке бэкапов' };
  }
  let obj: { groups?: unknown };
  try {
    obj = JSON.parse(text);
  } catch (err) {
    return { error: 'Файл не похож на mtrickle/json: ' + (err instanceof Error ? err.message : String(err)) };
  }
  const groups = Array.isArray(obj && obj.groups) ? (obj.groups as Record<string, unknown>[]) : null;
  if (!groups || !groups.length) return { error: 'В файле нет групп - импортировать нечего' };
  const ok: ImportGroupSrc[] = [];
  let bad = 0;
  for (const g of groups) {
    if (!g || typeof g.name !== 'string' || !g.name.trim() || typeof g.interface !== 'string' || !g.interface.trim()) {
      bad++;
      continue;
    }
    const rules = (Array.isArray(g.rules) ? (g.rules as Record<string, unknown>[]) : [])
      .filter((rl) => !!rl && typeof rl.rule === 'string' && !!rl.rule.trim() && typeof rl.type === 'string' && !!rl.type.trim())
      .map((rl) => ({
        type: String(rl.type),
        rule: String(rl.rule),
        enable: !!rl.enable,
        name: typeof rl.name === 'string' ? rl.name : '',
      }));
    ok.push({ name: g.name.trim(), color: typeof g.color === 'string' ? g.color : '', interface: g.interface, enable: !!g.enable, rules });
  }
  if (!ok.length) return { error: 'В файле нет групп с именем и интерфейсом' };
  return { name: fileName, groups: ok, bad };
}

// дубликаты правил внутри группы вычищаем сразу, счётчик - в отчёт
// (легаси 1929-1940)
export function dedupImportRules(groups: ImportGroupSrc[]): { groups: ImportGroupSrc[]; dedup: number } {
  let dedup = 0;
  const out = groups.map((g) => {
    const seen = new Set<string>();
    const rules: ImportGroupSrc['rules'] = [];
    for (const rl of g.rules) {
      const key = rl.type + ' ' + rl.rule.trim().toLowerCase();
      if (seen.has(key)) {
        dedup++;
        continue;
      }
      seen.add(key);
      rules.push({ type: rl.type, rule: rl.rule.trim(), enable: !!rl.enable, name: rl.name || '' });
    }
    return { ...g, rules };
  });
  return { groups: out, dedup };
}

// вердикт импорта (легаси 1948-1953)
export function importVerdict(
  mode: string,
  res: { added: number; rules: number; skipped?: string[] },
  dedup: number,
): string {
  const skipped = res.skipped || [];
  const clean = dedup ? `, вычищено дубликатов: ${dedup}` : '';
  if (mode === 'add') {
    let msg = `Импортировано групп: ${res.added} (правил: ${res.rules}${clean})`;
    if (skipped.length) msg += `. Пропущены дубли имени: ${skipped.join(', ')}`;
    return msg;
  }
  return `Список заменён: ${res.added} групп (правил: ${res.rules}${clean})`;
}

// mtrickle-groups-YYYYMMDD-HHMMSS.mtrickle (легаси 1998-1999)
export function exportStamp(d: Date): string {
  const p = (n: number) => String(n).padStart(2, '0');
  return String(d.getFullYear()) + p(d.getMonth() + 1) + p(d.getDate()) + '-' + p(d.getHours()) + p(d.getMinutes()) + p(d.getSeconds());
}

// бейдж и текст confirm восстановления по имени файла (легаси 1803, 1813-1816)
export function backupKind(name: string): { label: string; restoreAsk: string } {
  if (name.startsWith('mawg-full-')) {
    return {
      label: 'полный',
      restoreAsk:
        'ПОЛНОЕ восстановление панели: настройки (порт, доступ, RCI), пулы, каскады, наборы, правила, конфиг MagiTrickle. Панель перезапустится - если порт или адрес в бэкапе другие, открывайте её по адресу из бэкапа',
    };
  }
  if (name.startsWith('mt-config-')) {
    return {
      label: 'MT-конфиг',
      restoreAsk: 'сырой MT-конфиг: файл вернётся на место, демон MagiTrickle перезапустится',
    };
  }
  return { label: 'правила (shadow)', restoreAsk: 'правила из shadow: ВСЕ группы будут заменены на сохранённые' };
}
