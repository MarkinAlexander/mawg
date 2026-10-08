<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../api'

interface Pool {
  name: string
  device: string
  mode: string
  lastResult: string
  disabled: boolean
}
interface Status {
  version: string
  platform: string
  pools: Pool[]
  engine?: { running: boolean; version: string; lx: boolean; mode: string }
}

const st = ref<Status | null>(null)
const err = ref('')

async function load() {
  try {
    st.value = await api<Status>('GET', '/api/v1/status')
    err.value = ''
  } catch (e) {
    err.value = e instanceof Error ? e.message : String(e)
  }
}
onMounted(load)
setInterval(load, 5000)
</script>

<template>
  <div v-if="err" class="err">{{ err }}</div>
  <div v-else-if="st">
    <p class="muted">
      mawg {{ st.version }} ({{ st.platform }})
      <span v-if="st.engine">
        | движок sing-box {{ st.engine.version }} (lx: {{ st.engine.lx ? 'да' : 'нет' }},
        {{ st.engine.mode }}, {{ st.engine.running ? 'работает' : 'остановлен' }})
      </span>
    </p>
    <table>
      <tr><th>пул</th><th>интерфейс</th><th>режим</th><th>проверка</th></tr>
      <tr v-for="p in st.pools" :key="p.name">
        <td>{{ p.name }}</td>
        <td>{{ p.device }}</td>
        <td>{{ p.disabled ? 'выключен' : p.mode }}</td>
        <td>{{ p.lastResult || '-' }}</td>
      </tr>
    </table>
  </div>
  <p v-else class="muted">загрузка...</p>
</template>

<style scoped>
.muted { color: #7c8590; }
</style>
