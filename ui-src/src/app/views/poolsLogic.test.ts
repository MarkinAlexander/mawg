import { describe, expect, it } from 'vitest'
import type { SlotInfo } from '../../api/types'
import { fallbackHint, inspectLine, planView, protoGate, slotGate, slotOptions } from './poolsLogic'
import { probeHuman } from './groupsLogic'

const slot = (over: Partial<SlotInfo>): SlotInfo => ({
  id: 'nwg0', device: 'nwg0', description: '', linkUp: false, connected: false, managed: false, ...over,
});

describe('poolsLogic - слоты', () => {
  it('slotOptions: занятые пулами прячутся, busy-пометка, первый свободный', () => {
    const { options, firstFree, firstAny } = slotOptions(
      [
        slot({ id: 'nwg0', device: 'nwg0' }),
        slot({ id: 'nwg1', device: 'nwg1', description: 'провайдер', linkUp: true }),
        slot({ id: 'nwg2', device: 'nwg2' }),
      ],
      new Set(['nwg0']),
    );
    expect(options).toHaveLength(2);
    expect(options[0].label).toBe('nwg1 (nwg1) провайдер - занят');
    expect(options[0].busy).toBe(true);
    expect(options[1].label).toBe('nwg2 (nwg2) - свободен');
    expect(firstFree).toBe('nwg2');
    expect(firstAny).toBe('nwg1');
  });

  it('slotGate: free - офф, cfg с занятым - предупреждение замены', () => {
    expect(slotGate('free', null)).toEqual({
      off: true,
      text: 'Пул Amnezia Free идёт через ядро sing-box (tun-интерфейс) - слот WireGuard не нужен.',
    });
    expect(slotGate('cfg', null, true).text).toContain('Слот занят');
    expect(slotGate('cfg', null, false).text).toBe('');
  });

  it('slotGate: link - по inspect (awg-ключ / ошибка / нативные / только прокси)', () => {
    expect(slotGate('link', { amnezia: { serviceType: 'premium', serviceProtocol: 'awg' }, native: 0, engine: 0 }).text)
      .toContain('Протокол из ключа: AWG');
    expect(slotGate('link', { error: 'bad scheme', native: 0, engine: 0 }))
      .toEqual({ off: true, text: 'Ссылка не разбирается: bad scheme' });
    expect(slotGate('link', null).text).toContain('Вставьте ссылку');
    expect(slotGate('link', { native: 3, engine: 1, types: ['vless'] }).text)
      .toContain('Есть WG/AWG узлы (3)');
    expect(slotGate('link', { native: 0, engine: 2, types: ['vless'] }))
      .toEqual({
        off: true,
        text: 'Прокси-узлы (2) - будет создан tun-интерфейс движка sing-box (tun1, tun2...), слот не нужен.',
      });
  });

  it('inspectLine: все части через « · »', () => {
    expect(inspectLine({
      native: 2, engine: 1, types: ['vless', 'trojan'], intervalHours: 6,
      amnezia: { serviceType: 'premium', serviceProtocol: 'awg' },
    })).toBe('ключ Amnezia PREMIUM API · WG/AWG узлов: 2 · прокси-узлов: 1 (vless, trojan) · интервал подписки: 6 ч');
    expect(inspectLine({ native: 0, engine: 0 })).toBe('узлов не найдено');
    expect(inspectLine({ error: 'x', native: 0, engine: 0 })).toBe('не разбирается: x');
  });
});

describe('poolsLogic - вопрос о протоколе (парсинг до вопросов)', () => {
  it('cfg - показывается, free - нет', () => {
    expect(protoGate('cfg', null)).toEqual({ show: true, proto: '' });
    expect(protoGate('free', null)).toEqual({ show: false, proto: '' });
  });

  it('link: без разбора/ошибка/только движковые узлы - вопроса нет', () => {
    expect(protoGate('link', null).show).toBe(false);
    expect(protoGate('link', { error: 'bad', native: 0, engine: 0 }).show).toBe(false);
    expect(protoGate('link', { native: 0, engine: 2, types: ['vless'] }).show).toBe(false);
  });

  it('link: WG/AWG-узлы - селект с предвыбором из типов', () => {
    expect(protoGate('link', { native: 1, engine: 0, types: ['amneziawg'] }))
      .toEqual({ show: true, proto: 'amneziawg' });
    expect(protoGate('link', { native: 1, engine: 0, types: ['wireguard'] }))
      .toEqual({ show: true, proto: 'wireguard' });
    expect(protoGate('link', { native: 2, engine: 3, types: ['wireguard', 'vless'] }))
      .toEqual({ show: true, proto: 'wireguard' });
  });

  it('link: ключ Amnezia - vless без вопроса, awg с вопросом', () => {
    expect(protoGate('link', { amnezia: { serviceType: 'free', serviceProtocol: 'vless' }, native: 0, engine: 0 }).show)
      .toBe(false);
    expect(protoGate('link', { amnezia: { serviceType: 'premium', serviceProtocol: 'awg' }, native: 0, engine: 0 }).show)
      .toBe(true);
  });
});

describe('poolsLogic - план источника', () => {
  it('tun-план с пробой и предупреждениями; premium; cfg с дубликатами; без узлов', () => {
    const tun = planView({ pool: 'sub', tun: 'tun1', applied: 5, probeOk: true, probeMs: 120, warnings: ['нет lx-ядра'] });
    expect(tun.created).toBe('tun');
    expect(tun.summary).toBe('Пул sub создан: tun tun1, узлов 5 · проба: ok (120ms). нет lx-ядра');

    const prem = planView({ pool: 'prem', premium: true });
    expect(prem.created).toBe('premium');
    expect(prem.summary).toContain('Сменить страну');

    const cfg = planView({ pool: 'p', added: 3, duplicates: ['a.conf'] });
    expect(cfg.created).toBe('cfg');
    expect(cfg.summary).toContain('конфигов 3, дубликаты пропущены: a.conf');

    const none = planView({ engine: [{ tag: 'v1', type: 'vless' }] });
    expect(none.created).toBe('none');
    expect(none.summary).toContain('нет WG/AWG узлов');
  });

  it('amnezia-метаданные выданного конфига', () => {
    const pv = planView({
      pool: 'free', tun: 'tun1',
      amnezia: { protocol: 'awg', serverCountry: 'nl', serverCountryName: 'Нидерланды', maxDevices: 3, activeDevices: 1, issuedConfigs: 2 },
    });
    expect(pv.amneziaMeta).toBe('Выдан конфиг Amnezia (AWG), локация: Нидерланды, устройств: 1/3, выдано конфигов: 2');
    expect(pv.engineSavedForLx).toBe(true);
  });
});

describe('poolsLogic - подсказка фоллбека', () => {
  it('iface с пробой и без; пул - пустая', () => {
    const lbl = (d: string) => d;
    const ifaces = [{ device: 'nwg7', probe: {} }, { device: 'wg11' }];
    expect(fallbackHint('iface:nwg7', ifaces, lbl)).toContain('вернутся, когда пул оживет');
    expect(fallbackHint('iface:wg11', ifaces, lbl)).toContain('нет проверки здоровья');
    expect(fallbackHint('direct', ifaces, lbl)).toBe('');
    expect(fallbackHint('pool:warp', ifaces, lbl)).toBe('');
  });
});

describe('probeHuman (словарь проб rotator.go)', () => {
  it('ok/lossy/выше порога/link down/settling/ошибка; сырая строка сохраняется', () => {
    expect(probeHuman(undefined)).toContain('ещё нет');
    expect(probeHuman('ok (handshake 67s ago, icmp 13ms)')).toContain('туннель работает');
    expect(probeHuman('проба: ok (30ms)')).toContain('туннель работает');
    expect(probeHuman('ok (handshake 67s ago, icmp lossy)')).toContain('пинг выше порога (lossy)');
    expect(probeHuman('icmp 900ms выше порога 400ms')).toContain('отказ: пинг выше порога');
    expect(probeHuman('link down')).toContain('интерфейс опущен');
    expect(probeHuman('applied, settling')).toContain('стабилизация');
    expect(probeHuman('probe 1.2.3.4 failed')).toContain('ошибка проверки');
    expect(probeHuman('ok (handshake 1s ago, icmp lossy)')).toContain('ok (handshake 1s ago, icmp lossy)');
  });
});
