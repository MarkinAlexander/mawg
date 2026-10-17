// Чистая логика пикера цвета (UiColor): hex <-> HSV, нормализация ввода.
// Без DOM - для юнит-тестов.

export interface Hsv {
  h: number // 0..360
  s: number // 0..1
  v: number // 0..1
}

// '#ABC' | 'ABC' -> '#aabbcc'; '#AABBCC' -> '#aabbcc'; мусор -> null
export function normalizeHex(raw: string): string | null {
  const s = String(raw ?? '').trim().replace(/^#/, '').toLowerCase();
  if (/^[0-9a-f]{3}$/.test(s)) return '#' + s[0] + s[0] + s[1] + s[1] + s[2] + s[2];
  if (/^[0-9a-f]{6}$/.test(s)) return '#' + s;
  return null;
}

export function isValidHex(raw: string): boolean {
  return normalizeHex(raw) !== null;
}

export function hexToHsv(hex: string): Hsv {
  const norm = normalizeHex(hex) ?? '#000000';
  const r = Number.parseInt(norm.slice(1, 3), 16) / 255;
  const g = Number.parseInt(norm.slice(3, 5), 16) / 255;
  const b = Number.parseInt(norm.slice(5, 7), 16) / 255;
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const d = max - min;
  let h = 0;
  if (d > 0) {
    if (max === r) h = ((g - b) / d) % 6;
    else if (max === g) h = (b - r) / d + 2;
    else h = (r - g) / d + 4;
    h *= 60;
    if (h < 0) h += 360;
  }
  return { h, s: max === 0 ? 0 : d / max, v: max };
}

export function hsvToHex({ h, s, v }: Hsv): string {
  const hh = ((h % 360) + 360) % 360;
  const c = v * s;
  const x = c * (1 - Math.abs(((hh / 60) % 2) - 1));
  const m = v - c;
  let rgb: [number, number, number];
  if (hh < 60) rgb = [c, x, 0];
  else if (hh < 120) rgb = [x, c, 0];
  else if (hh < 180) rgb = [0, c, x];
  else if (hh < 240) rgb = [0, x, c];
  else if (hh < 300) rgb = [x, 0, c];
  else rgb = [c, 0, x];
  const to = (f: number) => Math.round((f + m) * 255)
    .toString(16)
    .padStart(2, '0');
  return '#' + to(rgb[0]) + to(rgb[1]) + to(rgb[2]);
}

// пресеты для свотчей: цвета панели и типовые групповые
export const COLOR_PRESETS: string[] = [
  '#4a9eff', '#0984e3', '#2aabee', '#00cec9',
  '#3fb96a', '#00b894', '#d9a032', '#e17055',
  '#e0564b', '#e84393', '#fd79a8', '#8956e6',
  '#6c5ce7', '#8a8f98', '#b8bec9', '#22303f',
];
