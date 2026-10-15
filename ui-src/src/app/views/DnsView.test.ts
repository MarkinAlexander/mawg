import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import DnsView from './DnsView.vue'
import type { DnsState } from '../../api/types'

// DNS-раздел (46-док §3, срез OpenWrt 64-док): состояние DNS-стека
// + переключение dnsmasq ↔ MT.

const statePayload: DnsState = {
  dnsmasq: { servers: ['127.0.0.1#3553'], inMT: true, noresolv: false, cacheSize: 1000, strict: false },
  mt: { upstream: '8.8.8.8:53', upstreams: ['8.8.8.8:53', '77.88.8.8:53'], remap53: true },
}

function apiStub(state: DnsState = statePayload) {
  return vi.fn(async (url: string) => {
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
    const stub = apiStub()
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
    const stub = apiStub()
    vi.stubGlobal('fetch', stub)
    const w = mount(DnsView)
    await vi.waitFor(() => expect(w.find('#card-dns .kv').exists()).toBe(true))

    await w.findAll('#card-dns .uibtn')[0].trigger('click')
    // POST ушёл и состояние перечитано (второй /dns/state)
    await vi.waitFor(() => {
      const postCalls = stub.mock.calls.filter((c: string[]) => (c[0] as string).endsWith('/dns/dnsmasq'))
      const stateCalls = stub.mock.calls.filter((c: string[]) => (c[0] as string).endsWith('/dns/state'))
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
    vi.stubGlobal('fetch', apiStub(strict))
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
