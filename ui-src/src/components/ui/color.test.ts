import { describe, expect, it } from 'vitest'
import { COLOR_PRESETS, hexToHsv, hsvToHex, isValidHex, normalizeHex } from './color'

describe('normalizeHex/isValidHex', () => {
  it('приводит 3/6-формы и отбрасывает мусор', () => {
    expect(normalizeHex('#4a9eff')).toBe('#4a9eff');
    expect(normalizeHex('4A9EFF')).toBe('#4a9eff');
    expect(normalizeHex('#abc')).toBe('#aabbcc');
    expect(normalizeHex('abc')).toBe('#aabbcc');
    expect(normalizeHex('  #ff0 ')).toBe('#ffff00');
    expect(normalizeHex('#12345')).toBeNull();
    expect(normalizeHex('zzz')).toBeNull();
    expect(normalizeHex('')).toBeNull();
    expect(isValidHex('#ff0')).toBe(true);
    expect(isValidHex('нет')).toBe(false);
  });
});

describe('hex<->HSV', () => {
  it('опорные точки', () => {
    expect(hexToHsv('#ff0000')).toEqual({ h: 0, s: 1, v: 1 });
    expect(hexToHsv('#00ff00')).toEqual({ h: 120, s: 1, v: 1 });
    expect(hexToHsv('#0000ff')).toEqual({ h: 240, s: 1, v: 1 });
    expect(hexToHsv('#000000')).toEqual({ h: 0, s: 0, v: 0 });
    expect(hexToHsv('#ffffff')).toEqual({ h: 0, s: 0, v: 1 });
    const hsv4a9eff = hexToHsv('#4a9eff');
    expect(Math.round(hsv4a9eff.h)).toBe(212); // h дробный - целые градусы ломали round-trip
    expect(hsv4a9eff.s).toBeCloseTo(0.7098039215686274, 12);
    expect(hsv4a9eff.v).toBe(1);
  });
  it('обратная конвертация возвращает исходный hex', () => {
    for (const hex of ['#4a9eff', '#2AABEE', '#d9a032', '#e0564b', '#8956e6', '#8a8f98', '#123456']) {
      expect(hsvToHex(hexToHsv(hex))).toBe(hex.toLowerCase());
    }
  });
  it('hsvToHex: оттенки по 60 градусов - чистые цвета', () => {
    expect(hsvToHex({ h: 0, s: 1, v: 1 })).toBe('#ff0000');
    expect(hsvToHex({ h: 60, s: 1, v: 1 })).toBe('#ffff00');
    expect(hsvToHex({ h: 120, s: 1, v: 1 })).toBe('#00ff00');
    expect(hsvToHex({ h: 180, s: 1, v: 1 })).toBe('#00ffff');
    expect(hsvToHex({ h: 240, s: 1, v: 1 })).toBe('#0000ff');
    expect(hsvToHex({ h: 300, s: 1, v: 1 })).toBe('#ff00ff');
    expect(hsvToHex({ h: 360, s: 1, v: 1 })).toBe('#ff0000'); // нормализация угла
    expect(hsvToHex({ h: -60, s: 1, v: 1 })).toBe('#ff00ff'); // и отрицательного
  });
  it('пресеты - валидные hex', () => {
    for (const p of COLOR_PRESETS) expect(normalizeHex(p)).toBe(p.toLowerCase());
  });
});
