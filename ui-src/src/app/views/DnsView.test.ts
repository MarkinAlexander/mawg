import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import DnsView from './DnsView.vue'
import { useStatus } from '../../stores/status'
import type { DnsState, DnsPolicyRes, DnsUpstreamsRes, Status } from '../../api/types'

// DNS-раздел: состояние службы DNS + резервные серверы MT + правила
// автопереключения L1-L5. Формулировки - «язык задач», подробности в [?]
// (UiHelp, 67-док).

const statePayload: DnsState = {
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
      expect(capture.put).toEqual({ upstreams: ['1.1.1.1', '77.88.8.8:53'] })
    })
    await vi.waitFor(() => expect(w.find('#card-dns').text()).toContain('Редактировать список'))
    w.unmount()
  })
})

describe('DnsView - правила автопереключения L1-L5', () => {
  const policyPayload: DnsPolicyRes = {
    capable: true,
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
    const btn = (label: string) => w.findAll('button').find((b) => b.text().includes(label));
    await vi.waitFor(() => expect(btn('Сохранить и применить')).toBeTruthy());

    const areas = w.findAll('#card-dns-policy textarea');
    expect(areas.length).toBe(2);
    await areas[1].setValue('9.9.9.9\n149.112.112.112');
    await nextTick();
    await btn('Сохранить и применить')!.trigger('click');
    await vi.waitFor(() => {
      expect(capture.put).toEqual({
        enabled: true,
        primary: ['8.8.8.8'],
        tunnel: 'proton',
        fallback: ['9.9.9.9', '149.112.112.112'],
        yandex: true,
        provider: false,
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
