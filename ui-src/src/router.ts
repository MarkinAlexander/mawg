import { createRouter, createWebHashHistory } from 'vue-router'
import LegacyPanel from './views/LegacyPanel.vue'

// hash-history: серверного fallback не нужно; пока приложение - легаси-панель
// целиком, маршрутов нет (появятся при разборе панели на компоненты)
export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/:pathMatch(.*)*', component: LegacyPanel },
  ],
})
