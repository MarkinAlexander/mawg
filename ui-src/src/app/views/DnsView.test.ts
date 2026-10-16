import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import DnsView from './DnsView.vue'
import type { DnsState, DnsUpstreamsRes } from '../../api/types'

// DNS-раздел (46-док §3, срез OpenWrt 64-док): состояние DNS-стека
// + переключение dnsmasq ↔ MT; каскад апстримов MT - живой стейт и
// редактор (форк rev8+).

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
})

describe('DnsView - состояние DNS (OpenWrt, 64-док)', () => {
  it('грузит состояние, бейджи «в MT»/remap/апстримы, кнопка возврата', async () => {
    const stub = apiStub(statePayload, { capable: false })
    vi.stubGlobal('fetch', stub)
    const w = mount(DnsView)
    await nextTick()
    await vi.waitFor(() => expect(w.find('#card-dns .kv').exists()).toBe(true))

    const text = w.find('#card-dns').text()
    expect(text).toContain('в MT (127.0.0.1#3553)')
    expect(text).toContain(':53 → MT (DNSOR)')
    expect(text).toContain('8.8.8.8:53 → 77.88.8.8:53 (приоритет = порядок)')
    expect(w.find('#card-dns .uibtn').text()).toContain('Вернуть напрямую')
    w.unmount()
  })

  it('кнопка дергает POST /dns/dnsmasq с рестором и перечитывает состояние', async () => {
    const stub = apiStub(statePayload, { capable: false })
    vi.stubGlobal('fetch', stub)
    const w = mount(DnsView)
    await vi.waitFor(() => expect(w.find('#card-dns .kv').exists()).toBe(true))

    await w.findAll('#card-dns .uibtn')[0].trigger('click')
    // POST ушёл и состояние перечитано (второй /dns/state)
    await vi.waitFor(() => {
      const postCalls = stub.mock.calls.filter((c) => (c[0] as string).endsWith('/dns/dnsmasq'))
      const stateCalls = stub.mock.calls.filter((c) => (c[0] as string).endsWith('/dns/state'))
      expect(postCalls.length).toBe(1)
      expect(stateCalls.length).toBeGreaterThanOrEqual(2)
    })
    w.unmount()
  })

  it('строгий режим (форкоп-стиль) помечен как SPOF, не в MT - кнопка направления', async () => {
    const strict: DnsState = {
      dnsmasq: { servers: [], inMT: false, noresolv: true, cacheSize: 0, strict: true },
      mt: { upstream: '127.0.0.1:53', remap53: false },
    }
    vi.stubGlobal('fetch', apiStub(strict, { capable: false }))
    const w = mount(DnsView)
    await vi.waitFor(() => expect(w.find('#card-dns .kv').exists()).toBe(true))

    const text = w.find('#card-dns').text()
    expect(text).toContain('строгий режим (SPOF)')
    expect(text).toContain('напрямую (из resolvfile)')
    expect(w.find('#card-dns .uibtn').text()).toContain('Направить dnsmasq в MT')
    w.unmount()
  })

  it('платформа без DNS-менеджера - честная заглушка', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ error: 'платформа пока без DNS-менеджера (срез OpenWrt)' }), { status: 501 })))
    const w = mount(DnsView)
    await vi.waitFor(() => expect(w.find('#card-dns').text()).toContain('только для OpenWrt'))
    w.unmount()
  })
})

describe('DnsView - каскад апстримов MT (форк rev8+, 64-док §6)', () => {
  it('живой стейт: бейджи живой/мёртвый/активный, политика канарейки; дубль из конфига скрыт', async () => {
    vi.stubGlobal('fetch', apiStub(statePayload, cascadePayload))
    const w = mount(DnsView)
    // (не «каскад апстримов MT» - эта строка есть в subtitle карточки)
    await vi.waitFor(() => expect(w.find('#card-dns').text()).toContain('Редактировать каскад'))

    const text = w.find('#card-dns').text()
    expect(text).toContain('8.8.8.8:53')
    expect(text).toContain('77.88.8.8:53')
    expect(text).toContain('активный')
    expect(text).toContain('мёртвый')
    expect(text).toContain('вниз за 3 сбоя подряд, вверх за 2 чистых')
    // при живом каскаде строка апстримов из config.yaml скрыта (дубль)
    expect(text).not.toContain('8.8.8.8:53 → 77.88.8.8:53 (приоритет = порядок)')
    expect(text).toContain('Редактировать каскад')
    // причина мёртвого - в tooltip бейджа адреса
    const badges = w.findAll('#card-dns .uibadge')
    const dead = badges.find((b) => b.text() === '77.88.8.8:53')
    expect(dead?.attributes('title')).toContain('подмена: канонических адресов нет')
    w.unmount()
  })

  it('MT без rev8 - список из config.yaml и честный хинт вместо каскада', async () => {
    vi.stubGlobal('fetch', apiStub(statePayload, { capable: false }))
    const w = mount(DnsView)
    await vi.waitFor(() => expect(w.find('#card-dns .kv').exists()).toBe(true))

    const text = w.find('#card-dns').text()
    expect(text).toContain('требуют MagiTrickle 0.8.2-rev8+')
    expect(text).toContain('8.8.8.8:53 → 77.88.8.8:53 (приоритет = порядок)')
    expect(text).not.toContain('Редактировать каскад')
    w.unmount()
  })

  it('редактор: удалить/добавить/поднять, PUT с новым порядком, перечитка', async () => {
    const capture: { put?: unknown } = {}
    vi.stubGlobal('fetch', apiStub(statePayload, cascadePayload, capture))
    const w = mount(DnsView)
    const btn = (label: string) => w.findAll('#card-dns .uibtn').find((b) => b.text().includes(label))

    await vi.waitFor(() => expect(btn('Редактировать каскад')).toBeTruthy())
    await btn('Редактировать каскад')!.trigger('click')
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
    // после сохранения редактор закрылся, перечитано состояние и каскад
    await vi.waitFor(() => expect(w.find('#card-dns').text()).toContain('Редактировать каскад'))
    w.unmount()
  })
})
