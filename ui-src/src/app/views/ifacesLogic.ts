// Чистая логика карточки «Интерфейсы» + WAN-пробы - порт функций легаси
// panel.js (PROBE_DEFAULTS 809, probeTypeOf 810, wireProbeSwitch 822,
// probeStatusBadge 276) для юнит-тестов без DOM.

export const PROBE_DEFAULTS: Record<string, string> = {
  http: 'http://www.gstatic.com/generate_204',
  icmp: '1.1.1.1',
};

// http для URL, всё остальное (в т.ч. пустое) - icmp (легаси probeTypeOf)
export function probeTypeOf(target: string): 'http' | 'icmp' {
  return /^https?:\/\//.test(target) ? 'http' : 'icmp';
}

// смена типа пробы (легаси wireProbeSwitch): пустое значение и дефолт
// своего типа заменяются дефолтом нового, пользовательское остаётся.
// placeholder у поля всегда дефолт нового типа.
export function targetOnTypeChange(current: string, newType: string): string {
  const cur = current.trim();
  const curType = probeTypeOf(cur);
  if (!cur || cur === PROBE_DEFAULTS[curType]) return PROBE_DEFAULTS[newType] || '';
  return current;
}

// разбор probeStatus (легаси probeStatusBadge): короткий вердикт в строке,
// развёрнутая причина ошибки из скобок - по клику, иначе длинный
// dial tcp... растягивает карточку на полэкрана
export function probeBadgeOf(
  st: string,
): { short: string; detail: string; ok: boolean } {
  const ok = st.startsWith('проба: ок');
  if (ok || !st.includes('(')) return { short: st, detail: '', ok };
  const short = st.slice(0, st.indexOf('(')).trim();
  const detail = st.slice(st.indexOf('(') + 1).replace(/\)$/, '');
  return { short, detail, ok };
}
