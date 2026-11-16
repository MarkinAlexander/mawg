import { describe, expect, it } from 'vitest'
import { tunnelDepBannerOf } from './dnsKeenetic'

// контролька «DNS роутера завязан на туннели» (кейс 17.11: весь
// name-server в WireguardN - после ребута резолвер роутера молчит,
// пока туннель не поднимется)
describe('dnsKeenetic - tunnelDepBannerOf', () => {
  const dep = (up: boolean[]) => ({
    dependent: true,
    tunnels: up.map((u, i) => ({ interface: 'Wireguard' + (5 + i), up: u, servers: i === 0 ? 3 : 1 })),
  });

  it('нет вердикта: undefined, dependent=false, пустой список туннелей', () => {
    expect(tunnelDepBannerOf(undefined)).toBeNull();
    expect(tunnelDepBannerOf({ dependent: false, tunnels: dep([true]).tunnels })).toBeNull();
    expect(tunnelDepBannerOf({ dependent: true, tunnels: [] })).toBeNull();
  });

  it('хоть один туннель жив - warn с честным состоянием каждого', () => {
    const b = tunnelDepBannerOf(dep([true, false]));
    expect(b?.kind).toBe('warn');
    expect(b?.text).toContain('Простые DNS-серверы роутера привязаны к туннелям');
    expect(b?.text).toContain('Wireguard5 (3 сервера) - поднят');
    expect(b?.text).toContain('Wireguard6 (1 сервер) - ЛЕЖИТ');
    expect(b?.text).toContain('после перезагрузки резолвер роутера молчит');
  });

  it('все туннели лежат - err: резолвер роутера не работает', () => {
    const b = tunnelDepBannerOf(dep([false, false]));
    expect(b?.kind).toBe('err');
    expect(b?.text).toContain('Туннели с DNS роутера лежат');
    expect(b?.text).toContain('- ЛЕЖИТ');
  });

  it('один туннель с одним сервером - «сервер» без склонения', () => {
    const b = tunnelDepBannerOf({
      dependent: true,
      tunnels: [{ interface: 'Wireguard5', up: true, servers: 1 }],
    });
    expect(b?.text).toContain('Wireguard5 (1 сервер) - поднят');
  });
});
