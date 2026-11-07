// Чистые функции DNS-страницы (без Vue - тестируются напрямую, как groupsLogic).

// durToSec - секунды из Go-длительности MT ("10s" -> 10, "5m0s" -> 300,
// "1m30s" -> 90, "0s" -> 0); нечитаемое/пустое - null.
export function durToSec(s: string): number | null {
  const re = /(\d+)(ms|[smh])/g;
  let total = 0;
  let matched = false;
  let m: RegExpExecArray | null;
  while ((m = re.exec(s)) !== null) {
    matched = true;
    const n = +m[1];
    if (m[2] === 'ms') total += n / 1000;
    else if (m[2] === 's') total += n;
    else if (m[2] === 'm') total += n * 60;
    else total += n * 3600;
  }
  if (!matched) return null;
  return Math.round(total);
}

export function healthFieldValid(v: string, min: number, max: number): boolean {
  return /^[0-9]+$/.test(v) && +v >= min && +v <= max;
}

// тексты вердиктов проб DNS (свои серверы Keenetic, уровни L1-L5, зоны) -
// единые для всех карточек страницы
export const verdictRu: Record<string, string> = { clean: 'работает', spoof: 'ПОДМЕНА', noanswer: 'нет ответа', error: 'недоступен' };
export function verdictVariant(v: string): 'ok' | 'warn' | 'err' {
  return v === 'clean' ? 'ok' : v === 'error' ? 'err' : 'warn';
}

// многострочное поле -> строки адресов (trim, пустые выброшены)
export const linesOf = (s: string): string[] => s.split('\n').map((x) => x.trim()).filter(Boolean);
