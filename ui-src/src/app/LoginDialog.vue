<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { UiBanner, UiButton, UiDialog, UiInput } from '../components/ui'

// Ф5 «авторизация нового UI»: аналог легаси startGate/dlgAuth (panel.js
// 3049/2439). GET /status без сессии отвечает 401 -> status.authNeeded
// ставит open - диалог залочен (Esc/backdrop/«назад» не закрывают, как
// cancel-preventDefault в легаси). Ошибка - инлайн в диалоге (не тост,
// паритет), успех - location.reload(): замороженные 401-вызовы (client.ts)
// всё равно никогда не разрешатся, перезагрузка поднимает панель с сессией.
const props = defineProps<{ open: boolean }>()

const login = ref('')
const pass = ref('')
const err = ref('')
const busy = ref(false)
const passInput = ref<InstanceType<typeof UiInput> | null>(null)

watch(
  () => props.open,
  async (v) => {
    if (!v) return
    err.value = ''
    pass.value = ''
    await nextTick()
    // нативный showModal фокусирует первый контрол диалога - фокус пароля
    // ставим после него, как легаси (50мс, panel.js showLogin)
    setTimeout(() => passInput.value?.focus(), 50)
  },
)

// сырой fetch, как в легаси: 401 от /auth/* и так не замораживает
// клиент (исключение /auth/* в client.ts)
async function submit(): Promise<void> {
  if (busy.value) return
  busy.value = true
  err.value = ''
  try {
    const r = await fetch('/api/v1/auth/login', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ login: login.value.trim(), password: pass.value }),
    })
    if (r.ok) {
      window.location.reload()
      return
    }
    const d = (await r.json().catch(() => ({}))) as { error?: string }
    err.value = d.error || 'ошибка ' + r.status
  } catch (e) {
    err.value = 'ошибка сети: ' + (e instanceof Error ? e.message : String(e))
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <UiDialog
    :open="props.open"
    title="Вход в панель"
    :locked="true"
    :busy="busy"
  >
    <form
      class="loginform"
      @submit.prevent="submit"
    >
      <UiInput
        v-model="login"
        label="логин"
        autocomplete="username"
      />
      <UiInput
        ref="passInput"
        v-model="pass"
        label="пароль"
        type="password"
        autocomplete="current-password"
      />
      <UiBanner
        v-if="err"
        kind="err"
        :text="err"
      />
      <p class="hint">
        Пароль первичного входа - в файле first-auth.txt в каталоге базы
        панели на роутере. Сброс: <code>mawg -reset-auth</code> по SSH.
      </p>
      <UiButton
        type="submit"
        variant="primary"
        :busy="busy"
      >
        войти
      </UiButton>
    </form>
  </UiDialog>
</template>

<style scoped>
.hint {
  color: var(--dim);
  font-size: 12px;
  margin: 10px 0;
}
.hint code {
  font-family: ui-monospace, monospace;
  color: var(--text);
}
.loginform .uibtn {
  margin-top: 4px;
}
</style>
