import { describe, expect, it } from 'vitest'
import { addrSecretIn, collectHosts, endpointSecretIn, hostOf, lineSecretIn, ruleSecretIn } from './secrets'
import type { PoolView } from '../api/types'

// слово владельца 2026-10-15: скрываются адреса из конфигов, а не места -
// домены правил, публичные DNS и пробы остаются видимыми
const pool = (over: Partial<PoolView>): PoolView => ({
  name: 'p', platform: 'openwrt', device: 'nwg0', mode: 'up',
  activeFile: 'a.conf', activeEndpoint: '', lastResult: '', lastError: '',
  rotations: 0, disabled: false, consecFails: 0,
  settings: {}, configs: [], premium: { imported: false, countries: [] }, protos: [],
  ...over,
});

describe('secrets - IPv4 в произвольном тексте (слово владельца 2026-10-17)', () => {
  it('тег узла с адресом и IPv4-имя скрываются, домены и версии - нет', () => {
    expect(addrSecretIn('203.0.113.7|moscow-xhttp')).toBe(true);
    expect(addrSecretIn('conf-109.248.163.108')).toBe(true);
    expect(addrSecretIn('grpc-riga')).toBe(false);
    expect(addrSecretIn('sub.example.com|node')).toBe(false);
    expect(addrSecretIn('sing-box 1.14.2-lx.12')).toBe(false);
    expect(addrSecretIn('')).toBe(false);
    expect(addrSecretIn(undefined)).toBe(false);
  });
});

describe('secrets (адреса из конфигов)', () => {
  it('hostOf: host:port, подсеть, wildcard, домен; порт/маска срезаются', () => {
    expect(hostOf('193.32.248.85:51820')).toBe('193.32.248.85');
    expect(hostOf('149.154.160.0/20')).toBe('149.154.160.0');
    expect(hostOf('*.cdn.telegram.org')).toBe('cdn.telegram.org');
    expect(hostOf('vpn.example.com:443')).toBe('vpn.example.com');
    expect(hostOf('  T.ME ')).toBe('t.me');
    expect(hostOf('')).toBe('');
  });

  it('collectHosts: активный + конфиги ротации; «узлов: 4» и пустые не попадают', () => {
    const hosts = collectHosts([
      pool({ activeEndpoint: '193.32.248.85:51820', configs: [
        { file: 'a.conf', endpoint: '95.179.130.10:443', enabled: true },
        { file: 'b.conf', endpoint: '', enabled: true },
      ] }),
      pool({ name: 'free', activeEndpoint: 'узлов: 4', configs: [] }),
    ]);
    expect([...hosts].sort()).toEqual(['193.32.248.85', '95.179.130.10']);
  });

  it('endpointSecretIn: только адрес из конфигов, «узлов: 4» и «-» не скрываются', () => {
    const hosts = collectHosts([pool({ activeEndpoint: '1.2.3.4:51820' })]);
    expect(endpointSecretIn(hosts, '1.2.3.4:51820')).toBe(true);
    expect(endpointSecretIn(hosts, 'узлов: 4')).toBe(false);
    expect(endpointSecretIn(hosts, '-')).toBe(false);
    expect(endpointSecretIn(hosts, '')).toBe(false);
    expect(endpointSecretIn(hosts, '9.9.9.9:53')).toBe(false);
  });

  it('ruleSecretIn: hosts каскадов (subnet /32) скрываются, чужие подсети и домены - нет', () => {
    const hosts = collectHosts([pool({ activeEndpoint: '193.32.248.85:51820' })]);
    expect(ruleSecretIn(hosts, '193.32.248.85/32')).toBe(true);
    expect(ruleSecretIn(hosts, '193.32.248.85')).toBe(true);
    expect(ruleSecretIn(hosts, '149.154.160.0/20')).toBe(false);
    expect(ruleSecretIn(hosts, '1.1.1.1')).toBe(false);
    expect(ruleSecretIn(hosts, 't.me')).toBe(false);
    expect(ruleSecretIn(hosts, '*.cdn.telegram.org')).toBe(false);
    expect(ruleSecretIn(hosts, '')).toBe(false);
  });

  it('lineSecretIn: строка журнала/ошибки скрывается только с адресом из конфигов', () => {
    const hosts = collectHosts([pool({ activeEndpoint: '193.32.248.85:51820' })]);
    expect(lineSecretIn(hosts, 'ротация: dial tcp 193.32.248.85:51820: i/o timeout')).toBe(true);
    expect(lineSecretIn(hosts, 'handshake к 8.8.8.8 не прошёл')).toBe(false);
    expect(lineSecretIn(hosts, 'событие №89 (restore)')).toBe(false);
    expect(lineSecretIn(hosts, '')).toBe(false);
  });
});
