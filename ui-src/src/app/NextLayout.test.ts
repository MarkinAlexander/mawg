import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createRouter, createWebHashHistory } from 'vue-router'
import NextLayout from './NextLayout.vue'
import { TABS } from './tabs'

describe('NextLayout', () => {
  it('рендерит все вкладки как ссылки и ведёт в legacy', async () => {
    const router = createRouter({
      history: createWebHashHistory(),
      routes: [
        ...TABS.map((t) => ({ path: `/next/${t.id}`, component: { template: '<div />' } })),
        { path: '/:pathMatch(.*)*', component: { template: '<div />' } },
      ],
    })
    router.push('/next/overview')
    await router.isReady()
    const w = mount(NextLayout, { global: { plugins: [router] } })
    const links = w.findAll('nav a')
    expect(links.length).toBe(TABS.length + 1) // вкладки + «старая версия»
    expect(w.text()).toContain('Обзор')
    expect(links[links.length - 1].attributes('href')).toBe('#/')
    for (const t of TABS) {
      expect(links.map((l) => l.text())).toContain(t.label)
    }
  })
})
