import { createRouter, createWebHashHistory } from 'vue-router'
import LegacyPanel from './views/LegacyPanel.vue'

// hash-history: серверного fallback не нужно. Legacy-панель остаётся
// дефолтом до конца Фазы 3; новое UI живёт за /next/ (Фаза 0 в
// local-docs/44-ui-rewrite-plan.md). Маршруты вкладок ленивые; текущая
// сборка инлайнит чанки (single-file IIFE) - сетевая ленивость появится
// в Фазе 4 вместе с разбиением бандла.
export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    {
      path: '/next/',
      component: () => import('./app/NextLayout.vue'),
      children: [
        { path: '', redirect: '/next/overview' },
        { path: 'overview', component: () => import('./app/views/OverviewView.vue') },
        { path: 'groups', component: () => import('./app/views/GroupsView.vue') },
        { path: 'interfaces', component: () => import('./app/views/InterfacesView.vue') },
        { path: 'backups', component: () => import('./app/views/BackupsView.vue') },
        { path: 'dns', component: () => import('./app/views/DnsView.vue') },
        { path: 'system', component: () => import('./app/views/SystemView.vue') },
        { path: 'settings', component: () => import('./app/views/SettingsView.vue') },
        // Демо UI-кита: в TABS не входит, доступен только по прямому URL
        { path: 'kit', component: () => import('./app/views/KitView.vue') },
      ],
    },
    { path: '/:pathMatch(.*)*', component: LegacyPanel },
  ],
})
