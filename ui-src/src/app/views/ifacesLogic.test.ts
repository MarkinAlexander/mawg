import { describe, expect, it } from 'vitest'
import { PROBE_DEFAULTS, probeBadgeOf, probeTypeOf, targetOnTypeChange, poolProbeBadge } from './ifacesLogic'

describe('ifacesLogic', () => {
  it('probeTypeOf: URL - http, IP и пустое - icmp (легаси probeTypeOf)', () => {
    expect(probeTypeOf('http://www.gstatic.com/generate_204')).toBe('http');
    expect(probeTypeOf('https://example.com/')).toBe('http');
    expect(probeTypeOf('1.1.1.1')).toBe('icmp');
    expect(probeTypeOf('')).toBe('icmp');
  });

  it('targetOnTypeChange: дефолт и пустое меняются на дефолт нового типа, своё остаётся', () => {
    expect(targetOnTypeChange(PROBE_DEFAULTS.http, 'icmp')).toBe(PROBE_DEFAULTS.icmp);
    expect(targetOnTypeChange('', 'icmp')).toBe(PROBE_DEFAULTS.icmp);
    expect(targetOnTypeChange(PROBE_DEFAULTS.icmp, 'http')).toBe(PROBE_DEFAULTS.http);
    expect(targetOnTypeChange('8.8.8.8', 'http')).toBe('8.8.8.8'); // не дефолт - не трогаем
    expect(targetOnTypeChange('http://my.host/', 'icmp')).toBe('http://my.host/');
  });

  it('probeBadgeOf: ок и без скобок - целиком, с скобками - короткий+деталь', () => {
    expect(probeBadgeOf('проба: ок (45ms)')).toEqual({ short: 'проба: ок (45ms)', detail: '', ok: true });
    expect(probeBadgeOf('проба: ок')).toEqual({ short: 'проба: ок', detail: '', ok: true });
    expect(probeBadgeOf('проба: не прошла (dial tcp 1.1.1.1: i/o timeout)')).toEqual({
      short: 'проба: не прошла',
      detail: 'dial tcp 1.1.1.1: i/o timeout',
      ok: false,
    });
    expect(probeBadgeOf('проба: не прошла')).toEqual({ short: 'проба: не прошла', detail: '', ok: false });
  });

  it('poolProbeBadge: плашка как у внешних - проба: ок Nмс / не прошла', () => {
    expect(poolProbeBadge({})).toBeNull();
    expect(poolProbeBadge({ probeOk: null })).toBeNull();
    expect(poolProbeBadge({ probeOk: true, probeMs: 152, probeStatus: 'ok (handshake 3s ago, http 152ms)' }))
      .toEqual({ short: 'проба: ок 152мс', detail: 'handshake 3s ago, http 152ms', ok: true });
    expect(poolProbeBadge({ probeOk: true, probeStatus: 'ok (handshake 9s ago, icmp lossy)' }))
      .toEqual({ short: 'проба: ок', detail: 'handshake 9s ago, icmp lossy', ok: true });
    expect(poolProbeBadge({ probeOk: false, probeStatus: 'probe http://x failed' }))
      .toEqual({ short: 'проба: не прошла', detail: '', ok: false });
    expect(poolProbeBadge({ probeOk: false, probeMs: 900, probeStatus: 'http 900ms выше порога 500ms' }))
      .toEqual({ short: 'проба: не прошла', detail: '', ok: false });
  });
});
