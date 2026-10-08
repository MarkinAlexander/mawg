import { createRouter, createWebHashHistory } from 'vue-router'
import StatusView from './views/StatusView.vue'
import StubView from './views/StubView.vue'

// hash-history: серверу не нужен fallback на index.html
export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', component: StatusView },
    { path: '/:page(.*)', component: StubView },
  ],
})
