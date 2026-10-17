import { describe, expect, it } from 'vitest'
import { ipValid, lanSubnet24, panelTargetOrigin, subnet24 } from './settingsLogic'

describe('settingsLogic (порт легаси panel.js)', () => {
  it('ipValid: IPv4 и CIDR /8../../32, остальное нет', () => {
    expect(ipValid('192.168.0.82')).toBe(true);
    expect(ipValid('192.168.0.0/24')).toBe(true);
    expect(ipValid('10.0.0.1/8')).toBe(true);
    expect(ipValid('10.0.0.1/32')).toBe(true);
    expect(ipValid('10.0.0.1/7')).toBe(false);
    expect(ipValid('10.0.0.1/33')).toBe(false);
    expect(ipValid('300.1.1.1')).toBe(false);
    expect(ipValid('1.2.3')).toBe(false);
    expect(ipValid('1.2.3.4/24/24')).toBe(false);
    expect(ipValid('')).toBe(false);
  });

  it('subnet24: жадный срез хвоста - только для сравнения префиксов', () => {
    expect(subnet24('192.168.0.82')).toBe('192.168');
    expect(subnet24('192.168.0.0/24')).toBe('192.168');
    expect(subnet24('10.1.2.3')).toBe('10.1');
  });

  it('lanSubnet24: честный /24 по октетам; IPv6/пусто = ""', () => {
    expect(lanSubnet24('192.168.0.82')).toBe('192.168.0.0/24');
    expect(lanSubnet24('10.1.2.3')).toBe('10.1.2.0/24');
    expect(lanSubnet24('fe80::1')).toBe('');
    expect(lanSubnet24('300.1.1.1')).toBe('');
    expect(lanSubnet24(undefined)).toBe('');
  });

  it('panelTargetOrigin: тот же origin = null, смена порта/адреса = новый origin', () => {
    const loc = { protocol: 'http:', hostname: '192.168.0.1', origin: 'http://192.168.0.1:8090' };
    expect(panelTargetOrigin('0.0.0.0', 8090, loc)).toBeNull();
    expect(panelTargetOrigin('', 8090, loc)).toBeNull();
    expect(panelTargetOrigin('0.0.0.0', 8091, loc)).toBe('http://192.168.0.1:8091');
    expect(panelTargetOrigin('127.0.0.1', 8090, loc)).toBe('http://127.0.0.1:8090');
    expect(panelTargetOrigin('::', 8091, loc)).toBe('http://192.168.0.1:8091');
  });
});
