import { createRouter, createWebHashHistory } from 'vue-router'

// hash-history: серверного fallback не нужно. С удалением легаси (Ф3.9,
// local-docs/44-ui-rewrite-plan.md §5.9) новое UI - единственная панель:
// всё, кроме /next/*, редиректит на обзор. Маршруты вкладок ленивые;
// сборка инлайнит чанки (single-file IIFE) - сетевая ленивость появится
// в Фазе 4/5 вместе с разбиением бандла.
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
    { path: '/:pathMatch(.*)*', redirect: '/next/' },
  ],
})
