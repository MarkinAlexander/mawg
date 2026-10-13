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
  { value: 'direct', label: 'прямой ход (проверив интернет)' },
  { value: 'iface', label: 'переключить на запасной интерфейс' },
];

export const POLICY_SHORT: Record<string, string> = {
  direct: 'прямой ход',
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
