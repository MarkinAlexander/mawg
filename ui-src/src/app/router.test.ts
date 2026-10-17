import { describe, expect, it } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import App from '../App.vue'
import { router } from '../router'
import { TABS } from '../app/tabs'

// сквозной тест проводки: реальный роутер -> ленивые вкладки -> заглушка
describe('роутер /next/', () => {
  it('каждая вкладка рендерит свой заголовок', async () => {
    const w = mount(App, { global: { plugins: [router] } })
    for (const t of TABS) {
      await router.push(`/next/${t.id}`)
      await flushPromises()
      expect(w.text()).toContain(t.label)
    }
    // корень /next/ уводит на Обзор
    await router.push('/next/')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/next/overview')
  })

  it('легаси удалён: корень и любой посторонний путь редиректят на /next/overview', async () => {
    for (const from of ['/', '/whatever', '/some/old/path']) {
      await router.push(from)
      await flushPromises()
      expect(router.currentRoute.value.path).toBe('/next/overview')
    }
  })
})
