import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import LoginDialog from './LoginDialog.vue'

// jsdom: location.reload неперегружаем через присваивание - целиком
// подменяем свойство window.location (GOTCHAS: focus только с attachTo)
function stubReload(): ReturnType<typeof vi.fn> {
  const reload = vi.fn()
  Object.defineProperty(window, 'location', {
    value: { ...window.location, reload },
    writable: true,
    configurable: true,
  })
  return reload
}

function fetchOk() {
  return vi.fn(async () => new Response(JSON.stringify({ ok: true, token: 't' }), { status: 200 }))
}

function mountDlg(open = true) {
  return mount(LoginDialog, { props: { open }, attachTo: document.body })
}

beforeEach(() => {
  vi.stubGlobal('fetch', fetchOk())
  stubReload()
})
afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('LoginDialog (Ф5 авторизация)', () => {
  it('открытый диалог: форма логина+пароля, залочен (cancel не закрывает)', async () => {
    const w = mountDlg()
    await nextTick()
    const dlg = w.find('dialog')
    expect(dlg.exists()).toBe(true)
    expect(dlg.attributes('open')).toBeDefined()
    expect(w.text()).toContain('Вход в панель')
    expect(w.findAll('input')).toHaveLength(2)
    expect(w.text()).toContain('войти')
    // залочен: cancel (Esc) не снимает open и не эмитит update:open
    await dlg.trigger('cancel')
    await nextTick()
    expect(w.emitted('update:open')).toBeFalsy()
    expect(dlg.attributes('open')).toBeDefined()
    w.unmount()
  })

  it('успешный вход: POST с обрезанным логином, reload; ошибка 401 - инлайн без reload', async () => {
    const w = mountDlg()
    await nextTick()
    const inputs = w.findAll('input')
    await inputs[0].setValue('  admin  ')
    await inputs[1].setValue('secret1')
    await w.find('form').trigger('submit')
    await vi.waitFor(() => expect(window.location.reload).toHaveBeenCalled())
    const call = (fetch as ReturnType<typeof vi.fn>).mock.calls.find(
      (c) => (c[0] as string).endsWith('/auth/login'),
    )!
    expect(call[0]).toBe('/api/v1/auth/login')
    expect(JSON.parse(call[1]!.body)).toEqual({ login: 'admin', password: 'secret1' })
    w.unmount()

    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ error: 'неверный логин или пароль' }), { status: 401 })))
    const reload2 = stubReload()
    const w2 = mountDlg()
    await nextTick()
    await w2.findAll('input')[0].setValue('admin')
    await w2.findAll('input')[1].setValue('bad')
    await w2.find('form').trigger('submit')
    await vi.waitFor(() => expect(w2.text()).toContain('неверный логин или пароль'))
    expect(reload2).not.toHaveBeenCalled()
    w2.unmount()
  })

  it('ошибки: 401 без JSON -> «ошибка 401», сетевой сбой -> «ошибка сети»', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('oops', { status: 500 })))
    const w = mountDlg()
    await nextTick()
    await w.find('form').trigger('submit')
    await vi.waitFor(() => expect(w.text()).toContain('ошибка 500'))
    w.unmount()

    vi.stubGlobal('fetch', vi.fn(async () => { throw new Error('boom') }))
    const w2 = mountDlg()
    await nextTick()
    await w2.find('form').trigger('submit')
    await vi.waitFor(() => expect(w2.text()).toContain('ошибка сети: boom'))
    w2.unmount()
  })

  it('автофокус в поле пароля при открытии (attachTo, GOTCHAS)', async () => {
    const w = mount(LoginDialog, { props: { open: false }, attachTo: document.body })
    await nextTick()
    await w.setProps({ open: true })
    await nextTick()
    await vi.waitFor(() => {
      const el = document.activeElement
      expect(el?.tagName).toBe('INPUT')
      expect((el as HTMLInputElement).type).toBe('password')
    })
    w.unmount()
  })

  it('busy на время запроса: кнопка гаснет, повторный submit не уходит', async () => {
    let resolve!: (r: Response) => void
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((r) => { resolve = r })))
    const w = mountDlg()
    await nextTick()
    const form = w.find('form')
    await form.trigger('submit')
    await nextTick()
    expect(w.find('button[type=submit]').attributes('disabled')).toBeDefined()
    await form.trigger('submit')
    expect((fetch as ReturnType<typeof vi.fn>).mock.calls.filter((c) => (c[0] as string).endsWith('/auth/login'))).toHaveLength(1)
    resolve(new Response(JSON.stringify({ ok: true }), { status: 200 }))
    w.unmount()
  })
})
