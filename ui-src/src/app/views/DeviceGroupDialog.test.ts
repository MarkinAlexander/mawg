import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import DeviceGroupDialog from './DeviceGroupDialog.vue'
import { useGroups } from '../../stores/groups'
import { useStatus } from '../../stores/status'
import { useToast } from '../../stores/toast'
import type { PoolView, Status } from '../../api/types'

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

const pool = (name: string): PoolView => ({
  name, platform: 'openwrt', device: 'wg_' + name, mode: 'up', activeFile: '', activeEndpoint: '',
  lastResult: '', lastError: '', rotations: 0, disabled: false, consecFails: 0,
  settings: {}, configs: [], premium: { imported: false, countries: [] }, protos: [],
})

const statusFor = (pools: PoolView[]): Status => ({
  version: 'v', platform: 'openwrt', backendName: 'openwrt', authEnabled: false,
  nativeAWG3: false, mtSources: true, pools,
})

async function makeDialog(routes: Record<string, unknown>, pools: PoolView[], poolName?: string) {
  useStatus().state.data = statusFor(pools)
  const f = stub(routes)
  vi.stubGlobal('fetch', f)
  const dlg = mount(DeviceGroupDialog, { props: { open: false, poolName } })
  await dlg.setProps({ open: true })
  await nextTick()
  return { dlg, f }
}

beforeEach(() => {
  vi.unstubAllGlobals()
  useStatus().stop()
  useStatus().state.data = null
  useGroups().state.groups = []
  useToast().toasts.list.splice(0)
})

describe('DeviceGroupDialog', () => {
  it('пул предвыбран из props, кнопка залочена без адресов', async () => {
    const { dlg } = await makeDialog({ '/mt/groups': { available: true, groups: [] } }, [pool('warp'), pool('proton')], 'proton')
    expect(dlg.text()).toContain('Устройство целиком через пул')
    const create = dlg.findAll('button').find((b) => b.text() === 'Создать')!
    expect(create.attributes('disabled')).toBeDefined()
    // предвыбор пула - текущее значение селекта
    expect(dlg.findAll('.psel-btn').some((b) => b.text().includes('proton'))).toBe(true)
    dlg.unmount()
  })

  it('кривой IP подсвечен, правильный уходит в POST /mt/device-group', async () => {
    const { dlg, f } = await makeDialog({ '/mt/groups': { available: true, groups: [] } }, [pool('warp')], 'warp')
    const ta = dlg.find('textarea')
    await ta.setValue('192.168.1.50\nноситель')
    await nextTick()
    expect(dlg.text()).toContain('Не похоже на IP/подсеть')
    const create = dlg.findAll('button').find((b) => b.text() === 'Создать')!
    expect(create.attributes('disabled')).toBeDefined()

    await ta.setValue('192.168.1.50\n192.168.1.51')
    await nextTick()
    expect(dlg.text()).not.toContain('Не похоже на IP/подсеть')
    await create.trigger('click')
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/mt/device-group'))
      expect(post).toBeTruthy()
      expect(JSON.parse(post![1]!.body as string)).toEqual({
        pool: 'warp',
        ips: ['192.168.1.50', '192.168.1.51'],
      })
    })
    await vi.waitFor(() =>
      expect(useToast().toasts.list.some((t) => t.text.includes('через пул warp'))).toBe(true))
    dlg.unmount()
  })

  it('имя группы передаётся, когда задано', async () => {
    const { dlg, f } = await makeDialog({ '/mt/groups': { available: true, groups: [] } }, [pool('warp')], 'warp')
    await dlg.find('textarea').setValue('192.168.1.60')
    const nameInput = dlg.findAll('input').find((i) => (i.element as HTMLInputElement).value === '')!
    await nameInput.setValue('Мой телефон')
    const create = dlg.findAll('button').find((b) => b.text() === 'Создать')!
    await create.trigger('click')
    await vi.waitFor(() => {
      const post = f.mock.calls.find((c) => (c[0] as string).endsWith('/mt/device-group'))
      expect(post).toBeTruthy()
      expect(JSON.parse(post![1]!.body as string)).toMatchObject({ name: 'Мой телефон', ips: ['192.168.1.60'] })
    })
    dlg.unmount()
  })
})
