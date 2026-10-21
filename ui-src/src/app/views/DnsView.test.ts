import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import DnsView from './DnsView.vue'
import { useStatus } from '../../stores/status'
import type { DnsState, DnsPolicyRes, DnsUpstreamsRes, KeeneticOwn, Status } from '../../api/types'

// DNS-раздел: состояние службы DNS + резервные серверы MT + правила
// автопереключения L1-L5. Формулировки - «язык задач», подробности в [?]
// (UiHelp, 67-док).

const statePayload: DnsState = {
  platform: 'openwrt',
  dnsmasq: { servers: ['127.0.0.1#3553'], inMT: true, noresolv: false, cacheSize: 1000, strict: false },
  mt: { upstream: '8.8.8.8:53', upstreams: ['8.8.8.8:53', '77.88.8.8:53'], remap53: true },
}

const cascadePayload: DnsUpstreamsRes = {
  capable: true,
  state: {
    available: true,
    health: { enabled: true, interval: '10s', timeout: '2s', failCycles: 3, restoreCycles: 2, restoreGap: '5m0s' },
    upstreams: [
      { address: '8.8.8.8:53', alive: true, active: true, fails: 0, oks: 5, lastSwap: '0001-01-01T00:00:00Z' },
      {
        address: '77.88.8.8:53',
        alive: false,
        active: false,
        fails: 3,
        oks: 0,
        lastSwap: '2026-10-16T10:00:00Z',
        reason: 'подмена: канонических адресов нет',
      },
    ],
  },
}

function apiStub(state: DnsState = statePayload, cascade: DnsUpstreamsRes | null = null, capture?: { put?: unknown }) {
  return vi.fn(async (url: string, opts?: RequestInit) => {
    if (url.endsWith('/dns/upstreams')) {
      if ((opts?.method ?? 'GET') === 'PUT') {
        if (capture) capture.put = JSON.parse(String(opts?.body))
        return new Response(JSON.stringify({ ok: true }), { status: 200 })
      }
      if (cascade === null) {
        return new Response(JSON.stringify({ error: 'MT недоступен' }), { status: 502 })
      }
      return new Response(JSON.stringify(cascade), { status: 200 })
    }
    if (url.endsWith('/dns/policy')) {
      if ((opts?.method ?? 'GET') === 'PUT') {
        return new Response(JSON.stringify({ ok: true }), { status: 200 })
      }
      return new Response(JSON.stringify({ capable: false, policy: { enabled: false } }), { status: 200 })
    }
    if (url.endsWith('/dns/state')) {
      return new Response(JSON.stringify(state), { status: 200 })
    }
    if (url.endsWith('/dns/dnsmasq')) {
      return new Response(JSON.stringify({ ok: true, state }), { status: 200 })
    }
    return new Response(JSON.stringify({}), { status: 200 })
  })
}

vi.mock('../../stores/confirm', () => ({
  useConfirm: () => ({ confirm: async () => true }),
}))
vi.mock('../../stores/toast', () => ({
  useToast: () => ({ push: vi.fn() }),
}))

beforeEach(() => {
  vi.unstubAllGlobals()
  const s = useStatus()
  s.stop()
  s.state.data = null
})

describe('DnsView - состояние DNS (OpenWrt)', () => {
  const waitState = async (w: ReturnType<typeof mount>): Promise<void> => {
    // (строка «Надёжность» рендерится и без данных - ждём данные /dns/state)
    await vi.waitFor(() => expect(w.find('#card-dns').text()).toContain('Служба DNS'))
  }

  it('грузит состояние, бейджи «через MT»/перенаправление/серверы, кнопка возврата', async () => {
    const stub = apiStub(statePayload, { capable: false })
    vi.stubGlobal('fetch', stub)
    const w = mount(DnsView)
    await waitState(w)

    const text = w.find('#card-dns').text()
    expect(text).toContain('через MT (127.0.0.1#3553)')
    expect(text).toContain('весь :53 → MT')
    expect(text).toContain('8.8.8.8:53 → 77.88.8.8:53 (первый - главный)')
    expect(w.find('#card-dns .uibtn').text()).toContain('Вернуть на прямые DNS')
    // UiHelp разрешён компонентом, не <uihelp>-тегом (грабля забытого импорта)
    expect(w.find('#card-dns .uihelp').exists()).toBe(true)
    expect(w.find('#card-dns').html()).not.toContain('<uihelp')
    w.unmount()
  })

  it('кнопка дергает POST /dns/dnsmasq с рестором и перечитывает состояние', async () => {
    const stub = apiStub(statePayload, { capable: false })
    vi.stubGlobal('fetch', stub)
    const w = mount(DnsView)
    await waitState(w)

    await w.findAll('#card-dns .uibtn')[0].trigger('click')
    await vi.waitFor(() => {
      const postCalls = stub.mock.calls.filter((c) => (c[0] as string).endsWith('/dns/dnsmasq'))
      const stateCalls = stub.mock.calls.filter((c) => (c[0] as string).endsWith('/dns/state'))
      expect(postCalls.length).toBe(1)
      expect(stateCalls.length).toBeGreaterThanOrEqual(2)
    })
    w.unmount()
  })

  it('строгий режим помечен как SPOF, не в MT - кнопка направления', async () => {
    const strict: DnsState = {
      platform: 'openwrt',
      dnsmasq: { servers: [], inMT: false, noresolv: true, cacheSize: 0, strict: true },
      mt: { upstream: '127.0.0.1:53', remap53: false },
    }
    vi.stubGlobal('fetch', apiStub(strict, { capable: false }))
    const w = mount(DnsView)
    await waitState(w)

    const text = w.find('#card-dns').text()
    expect(text).toContain('строгий режим (SPOF)')
    expect(text).toContain('напрямую (серверы провайдера)')
    expect(w.find('#card-dns .uibtn').text()).toContain('Направить DNS через MT')
    w.unmount()
  })

  it('много своих серверов: чип компактный (счётчик), полный список - по клику [?]', async () => {
    // слово владельца: чип с десятками серверов разросся и вылез за карточку
    const many: DnsState = {
      platform: 'openwrt',
      dnsmasq: { servers: ['127.0.0.1#5054', '10.14.103.1', '8.8.4.4'], inMT: false, noresolv: false, cacheSize: 1500, strict: false },
      mt: { upstream: '127.0.0.1:53', remap53: true },
    }
    vi.stubGlobal('fetch', apiStub(many, { capable: false }))
    const w = mount(DnsView)
    await waitState(w)

    const text = w.find('#card-dns').text()
    expect(text).toContain('свои серверы: 3')
    expect(text).not.toContain('10.14.103.1') // список не растёт в чипе
    // [?] рядом с чипом (сосед-бейдж): в поповере весь список
    const mine = w.findAll('#card-dns .uihelp-wrap').find(
      (x) => (x.element.previousElementSibling as HTMLElement | null)?.classList.contains('uibadge'),
    )
    expect(mine).toBeTruthy()
    await mine!.find('button.uihelp').trigger('click')
    await nextTick()
    const pop = w.find('.uihelp-pop')
    expect(pop.exists()).toBe(true)
    expect(pop.text()).toContain('Свои серверы dnsmasq')
    expect(pop.text()).toContain('· 10.14.103.1')
    expect(pop.text()).toContain('· 8.8.4.4')
    w.unmount()
  })

  it('платформа без DNS-менеджера - честная заглушка', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ error: 'платформа пока без DNS-менеджера (срез OpenWrt)' }), { status: 501 })))
    const w = mount(DnsView)
    await vi.waitFor(() => expect(w.find('#card-dns').text()).toContain('пока реализовано для OpenWrt'))
    w.unmount()
  })
})

describe('DnsView - резервные серверы MT (форк rev8+)', () => {
  it('живой стейт: «работает сейчас»/«не работает», автопроверка; дубль из конфига скрыт', async () => {
    vi.stubGlobal('fetch', apiStub(statePayload, cascadePayload))
    const w = mount(DnsView)
    // (не по заголовку секции - он же в UiHelp-текстах; ждём кнопку данных)
    await vi.waitFor(() => expect(w.find('#card-dns').text()).toContain('Редактировать список'))

    const text = w.find('#card-dns').text()
    expect(text).toContain('8.8.8.8:53')
    expect(text).toContain('77.88.8.8:53')
    expect(text).toContain('работает сейчас')
    expect(text).toContain('не работает')
    expect(text).toContain('отключение при 3 сбоях подряд, восстановление после 2 успешных проверок')
    // при живом списке строка серверов из config.yaml скрыта (дубль)
    expect(text).not.toContain('8.8.8.8:53 → 77.88.8.8:53 (первый - главный)')
    // причина нерабочего - в tooltip бейджа адреса
    const badges = w.findAll('#card-dns .uibadge')
    const dead = badges.find((b) => b.text() === '77.88.8.8:53')
    expect(dead?.attributes('title')).toContain('подмена: канонических адресов нет')
    w.unmount()
  })

  it('знак вопроса [?] у «Резервных серверов» открывает подсказку с подробностями', async () => {
    vi.stubGlobal('fetch', apiStub(statePayload, cascadePayload))
    const w = mount(DnsView)
    await vi.waitFor(() => expect(w.find('#card-dns .subhead .uihelp').exists()).toBe(true))
    expect(w.find('#card-dns .uihelp-pop').exists()).toBe(false)
    await w.find('#card-dns .subhead .uihelp').trigger('click')
    expect(w.find('#card-dns .uihelp-pop').exists()).toBe(true)
    expect(w.find('#card-dns .uihelp-pop').text()).toContain('deb.torproject.org')
    await w.find('#card-dns .subhead .uihelp').trigger('click')
    expect(w.find('#card-dns .uihelp-pop').exists()).toBe(false)
    w.unmount()
  })

  it('MT без rev8 - список из config.yaml и честный хинт вместо списка', async () => {
    vi.stubGlobal('fetch', apiStub(statePayload, { capable: false }))
    const w = mount(DnsView)
    await vi.waitFor(() => expect(w.find('#card-dns').text()).toContain('требуют MagiTrickle 0.8.2-rev8+'))

    const text = w.find('#card-dns').text()
    expect(text).toContain('8.8.8.8:53 → 77.88.8.8:53 (первый - главный)')
    expect(text).not.toContain('Редактировать список')
    w.unmount()
  })

  it('редактор: удалить/добавить/поднять, PUT с новым порядком, перечитка', async () => {
    const capture: { put?: unknown } = {}
    vi.stubGlobal('fetch', apiStub(statePayload, cascadePayload, capture))
    const w = mount(DnsView)
    const btn = (label: string) => w.findAll('#card-dns .uibtn').find((b) => b.text().includes(label))

    await vi.waitFor(() => expect(btn('Редактировать список')).toBeTruthy())
    await btn('Редактировать список')!.trigger('click')
    await vi.waitFor(() => expect(w.findAll('#card-dns .editrow').length).toBe(2))

    // UiInput разрешён (GOTCHAS: забытый импорт тихо рендерит кастом-элемент)
    expect(w.find('#card-dns').html()).not.toContain('<uiinput')

    // удалить первый (8.8.8.8), добавить 1.1.1.1, поднять его наверх
    const rows = () => w.findAll('#card-dns .editrow')
    await rows()[0].findAll('.uibtn')[2].trigger('click') // ✕
    await vi.waitFor(() => expect(rows().length).toBe(1))
    await w.find('#card-dns input').setValue('1.1.1.1')
    await btn('Добавить')!.trigger('click')
    await vi.waitFor(() => expect(rows().length).toBe(2))
    await rows()[1].findAll('.uibtn')[0].trigger('click') // ↑ второй строки
    expect(rows()[0].text()).toContain('1.1.1.1')

    await btn('Сохранить')!.trigger('click')
    await vi.waitFor(() => {
      expect(capture.put).toEqual({ upstreams: ['1.1.1.1', '77.88.8.8:53'], confirm: false })
    })
    await vi.waitFor(() => expect(w.find('#card-dns').text()).toContain('Редактировать список'))
    w.unmount()
  })

  // петлезащита (openwrt, dnsmasq.inMT): адрес dnsmasq в списке -
  // подсветка «петля?» и PUT с подтверждением (предупреждение, слово
  // владельца 2026-10-18)
  it('петлезащита: 127.0.0.1:53 в списке подсвечен, PUT уходит с confirm:true', async () => {
    const capture: { put?: unknown } = {}
    vi.stubGlobal('fetch', apiStub(statePayload, cascadePayload, capture))
    const w = mount(DnsView)
    const btn = (label: string) => w.findAll('#card-dns .uibtn').find((b) => b.text().includes(label))

    await vi.waitFor(() => expect(btn('Редактировать список')).toBeTruthy())
    await btn('Редактировать список')!.trigger('click')
    await vi.waitFor(() => expect(w.findAll('#card-dns .editrow').length).toBe(2))

    await w.find('#card-dns input').setValue('127.0.0.1:53')
    await btn('Добавить')!.trigger('click')
    await vi.waitFor(() => expect(w.findAll('#card-dns .editrow').length).toBe(3))

    // строка подсвечена честным «петля?»
    const rows = () => w.findAll('#card-dns .editrow')
    expect(rows()[2].text()).toContain('петля')
    // кнопка сохранения доступна, но рядом предупреждение
    await vi.waitFor(() => expect(w.find('#card-dns').text()).toContain('спросит подтверждение'))

    await btn('Сохранить')!.trigger('click')
    await vi.waitFor(() => {
      expect(capture.put).toEqual({ upstreams: ['8.8.8.8:53', '77.88.8.8:53', '127.0.0.1:53'], confirm: true })
    })
    w.unmount()
  })
})

describe('DnsView - правила автопереключения L1-L5', () => {
  const policyPayload: DnsPolicyRes = {
    capable: true,
    dohCapable: true,
    policy: { enabled: true, primary: ['8.8.8.8'], tunnel: 'proton', fallback: ['1.1.1.1:5353'], yandex: true, provider: false },
    composed: ['8.8.8.8:53', '1.1.1.1:5353', '77.88.8.8:53'],
    active: ['8.8.8.8:53', '1.1.1.1:5353', '77.88.8.8:53'],
    tunnelGroup: { id: '597de595', interface: 'proton' },
  };

  function policyStub(capture?: { put?: unknown; probe?: boolean }) {
    return vi.fn(async (url: string, opts?: RequestInit) => {
      if (url.endsWith('/dns/policy/probe')) {
        if (capture) capture.probe = true;
        return new Response(JSON.stringify({ results: [
          { level: 'L2 туннель proton: 8.8.8.8:53', kind: 'udp', server: '8.8.8.8:53', verdict: 'clean', canonical: 5, total: 5, ms: 265 },
          { level: 'L4 Яндекс', kind: 'udp', server: '77.88.8.8:53', verdict: 'clean', canonical: 5, total: 5, ms: 22 },
        ] }), { status: 200 });
      }
      if (url.endsWith('/dns/policy')) {
        if ((opts?.method ?? 'GET') === 'PUT') {
          if (capture) capture.put = JSON.parse(String(opts?.body));
          return new Response(JSON.stringify({ ok: true }), { status: 200 });
        }
        return new Response(JSON.stringify(policyPayload), { status: 200 });
      }
      if (url.endsWith('/dns/upstreams')) {
        if ((opts?.method ?? 'GET') === 'PUT') return new Response(JSON.stringify({ ok: true }), { status: 200 });
        return new Response(JSON.stringify(cascadePayload), { status: 200 });
      }
      if (url.endsWith('/dns/state')) {
        return new Response(JSON.stringify(statePayload), { status: 200 });
      }
      if (url.endsWith('/status')) {
        return new Response(JSON.stringify({ pools: [{ name: 'proton' }, { name: 'warp' }] }), { status: 200 });
      }
      return new Response(JSON.stringify({}), { status: 200 });
    });
  }

  it('карточка: сохранённые правила в полях, цепочка/активно/туннель видны', async () => {
    useStatus().state.data = {
      version: 'v', platform: 'openwrt', backendName: 'openwrt', authEnabled: false,
      nativeAWG3: false, mtSources: true,
      pools: [{ name: 'proton' }, { name: 'warp' }] as Status['pools'],
    } as Status;
    vi.stubGlobal('fetch', policyStub());
    const w = mount(DnsView);
    await vi.waitFor(() => expect(w.find('#card-dns-policy').text()).toContain('настроен через proton'));
    const text = w.find('#card-dns-policy').text();
    expect(text).toContain('Автопереключение включено');
    expect(text).toContain('8.8.8.8:53 → 1.1.1.1:5353 → 77.88.8.8:53');
    expect(text).toContain('Активно сейчас');
    // селект туннеля резолвит текущее значение из пулов /status
    expect(text).toContain('proton');
    w.unmount();
  });

  it('Сохранить и применить: confirm -> PUT /dns/policy с собранным телом', async () => {
    const capture: { put?: unknown; probe?: boolean } = {};
    vi.stubGlobal('fetch', policyStub(capture));
    const w = mount(DnsView);
    // (селектор по карточке политики: у диалога зонных DNS в футере такая
    // же кнопка - общий поиск по кнопкам ловит её раньше данных)
    const btn = (label: string) => w.findAll('#card-dns-policy button').find((b) => b.text().includes(label));
    await vi.waitFor(() => expect(btn('Сохранить и применить')).toBeTruthy());

    // без правок кнопка недоступна (слово владельца 2026-10-18)
    const saveBtn = w.findAll('#card-dns-policy button').find((b) => b.text().includes('Сохранить и применить'))!;
    expect(saveBtn.attributes('disabled')).toBeDefined();

    const areas = w.findAll('#card-dns-policy textarea');
    expect(areas.length).toBe(2);
    await areas[1].setValue('9.9.9.9\n149.112.112.112');
    await nextTick();
    // правка есть - кнопка доступна
    expect(saveBtn.attributes('disabled')).toBeUndefined();
    await btn('Сохранить и применить')!.trigger('click');
    await vi.waitFor(() => {
      expect(capture.put).toEqual({
        enabled: true,
        primary: ['8.8.8.8'],
        tunnel: 'proton',
        fallback: ['9.9.9.9', '149.112.112.112'],
        yandex: true,
        provider: false,
        confirm: false,
      });
    });
    w.unmount();
  });

  it('Проверить все уровни: POST probe -> строки с вердиктами', async () => {
    const capture: { put?: unknown; probe?: boolean } = {};
    vi.stubGlobal('fetch', policyStub(capture));
    const w = mount(DnsView);
    const btn = (label: string) => w.findAll('button').find((b) => b.text().includes(label));
    await vi.waitFor(() => expect(btn('Проверить все уровни')).toBeTruthy());
    await btn('Проверить все уровни')!.trigger('click');
    await vi.waitFor(() => expect(capture.probe).toBe(true));
    await vi.waitFor(() => expect(w.find('#card-dns-policy').text()).toContain('L2 туннель proton: 8.8.8.8:53'));
    expect(w.find('#card-dns-policy .uibadge.v-ok').exists()).toBe(true);
    w.unmount();
  });

  it('MT rev11+: пресеты DoH/DoT дописывают адрес в основные (без дубля)', async () => {
    const capture: { put?: unknown } = {};
    vi.stubGlobal('fetch', policyStub(capture));
    const w = mount(DnsView);
    const btn = (label: string) => w.findAll('button').find((b) => b.text() === label);
    await vi.waitFor(() => expect(btn('Google DoT')).toBeTruthy());

    await btn('Google DoT')!.trigger('click');
    await btn('Google DoT')!.trigger('click'); // дубль не размножается
    await btn('Cloudflare DoH')!.trigger('click');
    await nextTick();
    const areas = w.findAll('#card-dns-policy textarea');
    const primaryEl = areas[0].element as HTMLTextAreaElement;
    expect(primaryEl.value).toContain('tls://8.8.8.8');
    expect(primaryEl.value).toContain('https://1.1.1.1/dns-query');
    expect(primaryEl.value.match(/tls:\/\/8\.8\.8\.8/g)).toHaveLength(1);

    await w.findAll('button').find((b) => b.text().includes('Сохранить и применить'))!.trigger('click');
    await vi.waitFor(() => {
      expect(capture.put).toMatchObject({ primary: ['8.8.8.8', 'tls://8.8.8.8', 'https://1.1.1.1/dns-query'] });
    });
    w.unmount();
  });

  it('MT rev10-: пресетов DoH/DoT нет', async () => {
    const noDoh = { ...policyPayload, dohCapable: false };
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.endsWith('/dns/policy')) return new Response(JSON.stringify(noDoh), { status: 200 });
      if (url.endsWith('/dns/state')) return new Response(JSON.stringify(statePayload), { status: 200 });
      if (url.endsWith('/dns/upstreams')) return new Response(JSON.stringify(cascadePayload), { status: 200 });
      if (url.endsWith('/status')) return new Response(JSON.stringify({ pools: [{ name: 'proton' }] }), { status: 200 });
      return new Response(JSON.stringify({}), { status: 200 });
    }));
    const w = mount(DnsView);
    await vi.waitFor(() => expect(w.find('#card-dns-policy').text()).toContain('настроен через proton'));
    expect(w.findAll('button').some((b) => b.text() === 'Google DoT')).toBe(false);
    w.unmount();
  });

  it('MT без rev8 - честный хинт вместо формы', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.endsWith('/dns/policy')) {
        return new Response(JSON.stringify({ capable: false, policy: { enabled: false } }), { status: 200 });
      }
      if (url.endsWith('/dns/state')) {
        return new Response(JSON.stringify(statePayload), { status: 200 });
      }
      if (url.endsWith('/dns/upstreams')) {
        return new Response(JSON.stringify({ capable: false }), { status: 200 });
      }
      return new Response(JSON.stringify({}), { status: 200 });
    }));
    const w = mount(DnsView);
    await vi.waitFor(() => expect(w.find('#card-dns-policy').text()).toContain('нужен MagiTrickle 0.8.2-rev8+'));
    expect(w.find('#card-dns-policy textarea').exists()).toBe(false);
    w.unmount();
  });
});

describe('DnsView - зонные DNS (форк rev13)', () => {
  const zonesPayload = {
    capable: true,
    zones: [
      { id: 'z1', zone: 'corp.example.com', upstreams: ['10.8.0.1:53', '10.8.0.2:53'], pool: 'proton', enable: true },
      { id: 'z2', zone: 'lab.corp.example.com', upstreams: ['tls://10.8.1.1:853'], enable: false },
      { id: 'z3', zone: 'old.example.net', upstreams: ['192.0.2.53:53'], fallbackOpen: true, enable: true },
    ],
    live: {
      available: true,
      zones: [
        { zone: 'corp.example.com', fallback: false, upstreams: [
          { address: '10.8.0.1:53', alive: true, active: true, fails: 0, oks: 9, lastSwap: '0001-01-01T00:00:00Z' },
          { address: '10.8.0.2:53', alive: false, active: false, fails: 3, oks: 0, lastSwap: '2026-10-20T10:00:00Z', reason: 'dial: i/o timeout' },
        ] },
      ],
    },
  };

  function zonesStub(capture?: { put?: unknown; probe?: string[] }) {
    return vi.fn(async (url: string, opts?: RequestInit) => {
      if (url.endsWith('/dns/zones') && (opts?.method ?? 'GET') === 'PUT') {
        if (capture) capture.put = JSON.parse(String(opts?.body));
        const sent = JSON.parse(String(opts?.body));
        return new Response(JSON.stringify({ ok: true, zones: sent.zones, warnings: [] }), { status: 200 });
      }
      if (url.endsWith('/dns/zones')) {
        return new Response(JSON.stringify(zonesPayload), { status: 200 });
      }
      if (url.includes('/dns/zones/') && url.endsWith('/probe')) {
        if (capture) capture.probe = (capture.probe || []).concat(url);
        return new Response(JSON.stringify({ results: [
          { level: 'зона corp.example.com: 10.8.0.1:53', kind: 'udp', server: '10.8.0.1:53', verdict: 'clean', canonical: 5, total: 5, ms: 34 },
        ] }), { status: 200 });
      }
      if (url.endsWith('/dns/state')) {
        return new Response(JSON.stringify(statePayload), { status: 200 });
      }
      if (url.endsWith('/dns/upstreams')) {
        return new Response(JSON.stringify({ capable: false }), { status: 200 });
      }
      if (url.endsWith('/dns/policy')) {
        return new Response(JSON.stringify({ capable: false, policy: { enabled: false } }), { status: 200 });
      }
      if (url.endsWith('/status')) {
        return new Response(JSON.stringify({ pools: [{ name: 'proton' }, { name: 'warp' }] }), { status: 200 });
      }
      if (url.endsWith('/mt/interfaces')) {
        return new Response(JSON.stringify({ interfaces: [
          { device: 'proton', title: 'пул proton' },   // дублирует пул
          { device: 'tun1', title: 'warp' },            // движковый пул - дублирует
          { device: 'work', title: '' },                // device = имя пула
          { device: 'wg9', title: 'ручной WG' },        // уникальный - остаётся
        ] }), { status: 200 });
      }
      return new Response(JSON.stringify({}), { status: 200 });
    });
  }

  it('карточка: зоны с бейджами пути/выключена/fallback, живые серверы зоны', async () => {
    useStatus().state.data = {
      version: 'v', platform: 'openwrt', backendName: 'openwrt', authEnabled: false,
      nativeAWG3: false, mtSources: true,
      pools: [{ name: 'proton' }, { name: 'warp' }] as Status['pools'],
    } as Status;
    vi.stubGlobal('fetch', zonesStub());
    const w = mount(DnsView);
    await vi.waitFor(() => expect(w.find('#card-dns-zones').text()).toContain('corp.example.com'));
    const text = w.find('#card-dns-zones').text();
    expect(text).toContain('lab.corp.example.com');
    expect(text).toContain('выключена');
    expect(text).toContain('через proton');
    expect(text).toContain('fallback');
    expect(text).toContain('10.8.0.1:53');
    expect(text).toContain('жив: 10.8.0.1:53');
    expect(text).toContain('мёртв: 10.8.0.2:53');
    // мёртвый - с причиной в tooltip
    const dead = w.findAll('#card-dns-zones .uibadge').find((b) => b.text().includes('мёртв: 10.8.0.2:53'));
    expect(dead?.attributes('title')).toContain('dial: i/o timeout');
    expect(w.find('#card-dns-zones').html()).not.toContain('<uibadge');
    w.unmount();
  });

  it('rev12 - честный хинт вместо карточки, кнопки нет', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.endsWith('/dns/zones')) {
        return new Response(JSON.stringify({ capable: false, zones: [] }), { status: 200 });
      }
      if (url.endsWith('/dns/state')) return new Response(JSON.stringify(statePayload), { status: 200 });
      return new Response(JSON.stringify({}), { status: 200 });
    }));
    const w = mount(DnsView);
    await vi.waitFor(() => expect(w.find('#card-dns-zones').text()).toContain('нужен MagiTrickle 0.8.2-rev13+'));
    expect(w.findAll('#card-dns-zones button').some((b) => b.text().includes('зона'))).toBe(false);
    w.unmount();
  });

  it('«+ зона»: диалог -> сохранение -> PUT со всей таблицей (новая запись в конце)', async () => {
    const capture: { put?: unknown; probe?: string[] } = {};
    useStatus().state.data = {
      version: 'v', platform: 'openwrt', backendName: 'openwrt', authEnabled: false,
      nativeAWG3: false, mtSources: true,
      pools: [{ name: 'proton' }] as Status['pools'],
    } as Status;
    vi.stubGlobal('fetch', zonesStub(capture));
    const w = mount(DnsView);
    const btn = (label: string) => w.findAll('#card-dns-zones button').find((b) => b.text().includes(label));
    await vi.waitFor(() => expect(btn('+ зона')).toBeTruthy());
    await btn('+ зона')!.trigger('click');

    const dlg = w.find('dialog');
    expect(dlg.exists()).toBe(true);
    const inputs = dlg.findAll('input');
    await inputs[0].setValue('new.corp.example.com.'); // хвостовая точка срежется
    const areas = dlg.findAll('textarea');
    await areas[0].setValue('10.9.0.1');
    await dlg.findAll('button').find((b) => b.text().includes('Сохранить и применить'))!.trigger('click');
    await vi.waitFor(() => {
      const put = capture.put as { zones: { zone: string; upstreams: string[]; enable: boolean }[] } | undefined;
      expect(put?.zones).toHaveLength(4);
      expect(put?.zones[3]).toMatchObject({ zone: 'new.corp.example.com', upstreams: ['10.9.0.1'], enable: true });
    });
    w.unmount();
  });

  it('«Проверить» по зоне: POST /dns/zones/{id}/probe -> вердикты в карточке зоны', async () => {
    const capture: { put?: unknown; probe?: string[] } = {};
    vi.stubGlobal('fetch', zonesStub(capture));
    const w = mount(DnsView);
    const btn = (label: string) => w.findAll('#card-dns-zones button').find((b) => b.text().includes(label));
    await vi.waitFor(() => expect(btn('Проверить')).toBeTruthy());
    await btn('Проверить')!.trigger('click');
    await vi.waitFor(() => expect(w.find('#card-dns-zones').text()).toContain('зона corp.example.com: 10.8.0.1:53'));
    expect((capture.probe || []).some((u) => u.includes('/dns/zones/z1/probe'))).toBe(true);
    // выключенной зоны «Проверить» недоступна (кнопки по одной на зону:
    // первая с таким текстом - у включённой z1)
    w.unmount();
  });

  it('дедуп пути: интерфейс, чьё устройство/подпись - имя пула, не дублируется (скрин друга)', async () => {
    useStatus().state.data = {
      version: 'v', platform: 'openwrt', backendName: 'openwrt', authEnabled: false,
      nativeAWG3: false, mtSources: true,
      pools: [{ name: 'proton' }, { name: 'warp' }, { name: 'work' }] as Status['pools'],
    } as Status;
    vi.stubGlobal('fetch', zonesStub());
    const w = mount(DnsView);
    const btn = (label: string) => w.findAll('#card-dns-zones button').find((b) => b.text().includes(label));
    await vi.waitFor(() => expect(btn('+ зона')).toBeTruthy());
    await btn('+ зона')!.trigger('click');
    await nextTick();
    const dlg = w.find('dialog');
    // открываем селект ОДНИМ кликом (клик внутри waitFor тогглит open/close)
    const trigger = dlg.findAll('button').find((b) => b.text().includes('напрямую'));
    await trigger!.trigger('click');
    await nextTick();
    const snap = dlg.text();
    // пулы по одному разу
    expect(snap.match(/proton/g)?.length).toBe(1);
    expect(snap.match(/warp/g)?.length).toBe(1);
    // iface-дубли пула выброшены (proton-девайс, tun1+warp, work-девайс)
    expect(snap).not.toContain('iface:proton');
    expect(snap).not.toContain('tun1');
    // уникальный готовый интерфейс остался
    expect(snap).toContain('wg9 - ручной WG');
    w.unmount();
  });
});

describe('DnsView - keenetic (71-док)', () => {
  const keenState: DnsState = {
    platform: 'keenetic',
    dnsmasq: { servers: [], inMT: false, noresolv: false, strict: false },
    mt: { upstream: '8.8.8.8:53', upstreams: ['8.8.8.8:53'], remap53: true },
  };

  it('перехват DNS вместо dnsmasq-разреза, без кнопок переключения, MT-часть на месте', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.endsWith('/dns/state')) {
        return new Response(JSON.stringify(keenState), { status: 200 });
      }
      if (url.endsWith('/dns/upstreams')) {
        return new Response(JSON.stringify(cascadePayload), { status: 200 });
      }
      if (url.endsWith('/dns/policy')) {
        return new Response(JSON.stringify({ capable: true, policy: { enabled: false } }), { status: 200 });
      }
      if (url.endsWith('/status')) {
        return new Response(JSON.stringify({ pools: [] }), { status: 200 });
      }
      return new Response(JSON.stringify({}), { status: 200 });
    }));
    const w = mount(DnsView);
    await vi.waitFor(() => expect(w.find('#card-dns').text()).toContain('Перехват DNS'));
    const card = w.find('#card-dns');
    // keenetic-разрез: нет dnsmasq-строк и кнопок переключения
    expect(card.text()).not.toContain('Служба DNS');
    expect(card.text()).not.toContain('Кэш адресов');
    expect(card.text()).not.toContain('Направить DNS через MT');
    expect(card.text()).not.toContain('Надёжность');
    expect(card.text()).toContain('Собственные серверы Keenetic панель не меняет');
    // [?] у перехвата, MT-часть переиспользована
    expect(card.html()).not.toContain('<uihelp');
    expect(card.text()).toContain('Резервные серверы');
    w.unmount();
  });
});

describe('DnsView - keenetic: собственные серверы (71-док §4.1)', () => {
  const ownPayload: KeeneticOwn = {
    doh: [{ uri: 'https://dns.google/dns-query', format: 'dnsm' }],
    dot: [{ address: '1.1.1.1' }],
    udp: [
      { interface: 'ISP', servers: ['8.8.8.8'] },
      { interface: 'Wireguard5', servers: ['8.8.8.8', '8.8.4.4'] },
    ],
  };

  function stubOwn(withOwn: boolean): void {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.endsWith('/dns/state')) {
        return new Response(JSON.stringify({
          platform: 'keenetic',
          dnsmasq: { servers: [], inMT: false, noresolv: false, strict: false },
          mt: { upstream: '8.8.8.8:53', remap53: true },
        }), { status: 200 });
      }
      if (url.endsWith('/dns/keenetic/own')) {
        return new Response(JSON.stringify(withOwn ? ownPayload : { doh: [], dot: [], udp: [] }), { status: 200 });
      }
      if (url.endsWith('/dns/upstreams')) {
        return new Response(JSON.stringify({ capable: false }), { status: 200 });
      }
      if (url.endsWith('/dns/policy')) {
        return new Response(JSON.stringify({ capable: false, policy: { enabled: false } }), { status: 200 });
      }
      if (url.includes('/dns/probe?')) {
        return new Response(JSON.stringify({ results: [
          { level: url, kind: 'udp', server: 'x', verdict: 'clean', canonical: 5, total: 5, ms: 10 },
        ] }), { status: 200 });
      }
      return new Response(JSON.stringify({}), { status: 200 });
    }));
  }

  it('список своих серверов с разбивкой по типам, проба даёт вердикты', async () => {
    stubOwn(true);
    const w = mount(DnsView);
    await vi.waitFor(() => expect(w.find('#card-dns').text()).toContain('Собственные серверы Keenetic'));
    const card = w.find('#card-dns');
    expect(card.text()).toContain('https://dns.google/dns-query');
    expect(card.text()).toContain('формат dnsm');
    expect(card.text()).toContain('1.1.1.1:853');
    expect(card.text()).toContain('подключение Wireguard5');
    expect(card.text()).not.toContain('записей не найдено');

    await card.findAll('.uibtn').find((b) => b.text() === 'Проверить')!.trigger('click');
    await vi.waitFor(() => expect(card.text()).toContain('работает'));
    w.unmount();
  });

  it('пустые типы - честные «записей не найдено»', async () => {
    stubOwn(false);
    const w = mount(DnsView);
    await vi.waitFor(() => expect(w.find('#card-dns').text()).toContain('записей не найдено'));
    const card = w.find('#card-dns').text();
    expect(card).toContain('DoH');
    expect(card).toContain('DoT');
    expect(card).toContain('Простые (UDP)');
    w.unmount();
  });
});

describe('DnsView - keenetic: совет «Усилить DNS» (71-док §3.2)', () => {
  it('с DoH/DoT - подтверждение основы, простой UDP только с оговоркой (дизайн-слово владельца)', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.endsWith('/dns/state')) {
        return new Response(JSON.stringify({
          platform: 'keenetic',
          dnsmasq: { servers: [], inMT: false, noresolv: false, strict: false },
          mt: { upstream: '8.8.8.8:53', remap53: true },
        }), { status: 200 });
      }
      if (url.endsWith('/dns/keenetic/own')) {
        return new Response(JSON.stringify({
          doh: [{ uri: 'https://dns.google/dns-query', format: 'dnsm' }],
          dot: [{ address: '1.1.1.1' }],
          udp: [{ interface: 'ISP', servers: ['8.8.8.8'] }],
        }), { status: 200 });
      }
      if (url.endsWith('/dns/upstreams') || url.endsWith('/dns/policy')) {
        return new Response(JSON.stringify({ capable: false, policy: { enabled: false } }), { status: 200 });
      }
      return new Response(JSON.stringify({}), { status: 200 });
    }));
    const w = mount(DnsView);
    await vi.waitFor(() => expect(w.find('#card-dns-advice').exists()).toBe(true));
    const advice = w.find('#card-dns-advice').text();
    expect(advice).toContain('Шифрованный DNS (DoH/DoT) настроен');
    expect(advice).not.toContain('запасным'); // вредный совет убран (слово владельца)
    expect(advice).toContain('держите их, только если уверены');
    expect(advice).toContain('зависит от настроек ТСПУ'); // честная оговорка про антиподмену
    // ссылка на антиподмену zator - живая
    const z = w.find('#card-dns-advice a[href*="github.com/AloofLibra/zator"]');
    expect(z.exists()).toBe(true);
    expect(z.text()).toContain('zator');
    w.unmount();
  });

  it('только простые UDP - warn «добавьте DoH или DoT»', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.endsWith('/dns/state')) {
        return new Response(JSON.stringify({
          platform: 'keenetic',
          dnsmasq: { servers: [], inMT: false, noresolv: false, strict: false },
          mt: { upstream: '8.8.8.8:53', remap53: true },
        }), { status: 200 });
      }
      if (url.endsWith('/dns/keenetic/own')) {
        return new Response(JSON.stringify({ doh: [], dot: [], udp: [{ interface: 'ISP', servers: ['8.8.8.8'] }] }), { status: 200 });
      }
      if (url.endsWith('/dns/upstreams') || url.endsWith('/dns/policy')) {
        return new Response(JSON.stringify({ capable: false, policy: { enabled: false } }), { status: 200 });
      }
      return new Response(JSON.stringify({}), { status: 200 });
    }));
    const w = mount(DnsView);
    await vi.waitFor(() => expect(w.find('#card-dns-advice').exists()).toBe(true));
    const advice = w.find('#card-dns-advice').text();
    // шифрованных нет - «основа» не показывается, но UDP-оговорки есть
    expect(advice).not.toContain('Шифрованный DNS (DoH/DoT) настроен');
    expect(advice).toContain('Простые UDP отвечают быстрее, но не защищены');
    expect(advice).toContain('держите их, только если уверены');
    const z = w.find('#card-dns-advice a[href*="github.com/AloofLibra/zator"]');
    expect(z.exists()).toBe(true);
    w.unmount();
  });
});
