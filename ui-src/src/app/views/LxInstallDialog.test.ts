import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import LxInstallDialog from './LxInstallDialog.vue'
import { useSystem } from '../../stores/system'

type ReqInit = { method?: string; body?: string };

function stub(routes: Record<string, unknown>) {
  return vi.fn(async (url: string, init?: ReqInit) => {
    const key = Object.keys(routes).find((k) => (url as string).endsWith(k))
    if (key) {
      const v = routes[key]
      const body = typeof v === 'function' ? (v as (init?: ReqInit) => unknown)(init) : v
      return new Response(JSON.stringify(body), { status: 200 })
    }
    return new Response('{}', { status: 200 })
  })
}

const sources = {
  sources: [
    { id: 'extended', title: 'sing-box-extended (форк shtorm-7)', note: 'форк shtorm-7', latest: 'v1.14.3-extended-2.7.3', upx: true, available: true },
    { id: 'lx', title: 'sing-box-lx (наш форк)', note: 'наш форк', latest: 'v1.14.2-lx.12', upx: true, available: true },
    { id: 'upstream', title: 'sing-box (официальный апстрим)', note: 'официальный', latest: 'v1.14.4', upx: false, available: true },
  ],
};

async function makeDialog(item: Record<string, unknown>) {
  const f = stub({
    '/system/check': { platform: 'openwrt', items: [{ id: 'singbox-lx', title: 'Ядро sing-box', installed: true, version: '1.14.2-lx.12', action: 'singbox-lx', actionLabel: 'обновить ядро', confirm: 'подтверждаю', ...item }] },
    '/cores/singbox/sources': sources,
    '/system/install': { ok: true, output: '- checksum ok\n- установлено' },
  })
  vi.stubGlobal('fetch', f)
  const dlg = mount(LxInstallDialog, { props: { open: false } })
  await dlg.setProps({ open: true })
  await vi.waitFor(() => {
    expect(dlg.text()).toContain('Источник сборки')
    // источники загружены - в опции виден апстрим из ответа
    expect(dlg.text()).not.toContain('источники уточняются')
  })
  return { dlg, f }
}

beforeEach(() => {
  vi.unstubAllGlobals()
  useSystem().stop()
  useSystem().state.data = null
  useSystem().cores.list.splice(0)
})

describe('LxInstallDialog (мультиисточник)', () => {
  it('дефолт источника - extended, установленное ядро предвыбрано своим источником', async () => {
    const { dlg: d1 } = await makeDialog({})
    // .psel-btn первого селекта показывает выбранное значение
    expect(d1.findAll('.psel-btn')[0].text()).toContain('sing-box-extended')
    d1.unmount()

    const { dlg } = await makeDialog({ source: 'lx' })
    expect(dlg.findAll('.psel-btn')[0].text()).toContain('sing-box-lx')
    dlg.unmount()
  })

  it('payload уносит источник и профиль, upstream прячет сжатый профиль', async () => {
    const { dlg, f } = await makeDialog({})
    // выбор апстрима: первый селект - источник
    await dlg.findAll('.psel-btn')[0].trigger('click')
    await nextTick()
    const up = dlg.findAll('.psel-item').find((o) => o.text().includes('официальный апстрим'))!
    await up.trigger('click')
    await nextTick()
    // у апстрима нет сжатого профиля - в селекте профиля только обычный
    const flavorBtn = dlg.findAll('.psel-btn')[1]
    expect(flavorBtn.text()).toContain('обычный')

    const install = dlg.findAll('button').find((b) => b.text() === 'обновить ядро')!
    await install.trigger('click')
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/system/install'))
      expect(post).toBeTruthy()
      expect(JSON.parse(post![1]!.body as string)).toEqual({
        id: 'singbox-lx', source: 'upstream', flavor: 'plain', backup: false,
      })
    })
    await vi.waitFor(() => expect(dlg.text()).toContain('Установлено'))
    dlg.unmount()
  })

  it('результат установки показывает источник (владелец путается, чьё ядро стоит)', async () => {
    const f = stub({
      '/system/check': { platform: 'openwrt', items: [{ id: 'singbox-lx', title: 'Ядро sing-box', installed: true, version: '1.14.2-lx.12', action: 'singbox-lx', actionLabel: 'обновить ядро', confirm: 'подтверждаю' }] },
      '/cores/singbox/sources': sources,
      '/system/install': { ok: true, output: '- установлено', source: 'Leadaxe/sing-box-lx', sourceTitle: 'sing-box-lx (оригинал Leadaxe)' },
    })
    vi.stubGlobal('fetch', f)
    const dlg = mount(LxInstallDialog, { props: { open: false } })
    await dlg.setProps({ open: true })
    await vi.waitFor(() => expect(dlg.text()).toContain('Источник сборки'))
    const install = dlg.findAll('button').find((b) => b.text() === 'обновить ядро')!
    await install.trigger('click')
    await vi.waitFor(() => {
      expect(dlg.text()).toContain('Установлено')
      expect(dlg.text()).toContain('источник: sing-box-lx (оригинал Leadaxe)')
    })
    dlg.unmount()
  })
})
