<script setup lang="ts">
import { computed, watch } from 'vue'
import { ref } from 'vue'
import { UiButton, UiDialog, UiHint, UiInput, UiSelect, UiTextarea, type UiSelectOption } from '../../components/ui'
import { useStatus } from '../../stores/status'
import { useToast } from '../../stores/toast'
import { useConfirm } from '../../stores/confirm'
import { useGroups } from '../../stores/groups'
import { ApiError } from '../../api/client'

// Диалог «устройство целиком через пул» (слово владельца 2026-10-28):
// один-несколько IP локальной сети → ВЕСЬ их трафик уходит в пул.
// Бэкенд создаёт группу MagiTrickle с sources=[IP] + статикой 0.0.0.0/0
// и ставит её ПОСЛЕДНЕЙ в списке - при пересечении с группами назначений
// (домены/гео) побеждает она. Доступ к роутеру сохраняется: его адреса
// обслуживаются local-таблицей маршрутизации раньше туннеля.
// Коллизия адреса (уже в другом пуле / в исключениях) - 409 с сервера:
// предупреждение со списком + подтверждение «Всё равно привязать»
// (97-док, паттерн петлезащиты DnsView).
const open = defineModel<boolean>('open', { default: false })
const props = defineProps<{ poolName?: string }>()

const { state: status } = useStatus()
const { push } = useToast()
const { confirm } = useConfirm()
const groups = useGroups()

const ips = ref('')
const name = ref('')
const pool = ref('')
const busy = ref(false)

const poolOptions = computed<UiSelectOption[]>(() =>
  (status.data?.pools || [])
    .filter((p) => !p.disabled)
    .map((p) => ({ value: p.name, label: p.name + (p.device && p.device !== p.name ? ` (${p.device})` : '') })))

watch(open, (v) => {
  if (!v) return
  ips.value = ''
  name.value = ''
  const preset = props.poolName && poolOptions.value.some((o) => o.value === props.poolName)
    ? props.poolName
    : poolOptions.value[0]?.value || ''
  pool.value = preset
})

// адреса: по одному в строке (или через запятую/пробел)
const parsed = computed(() =>
  ips.value.split(/[\n,;]+/).map((s) => s.trim()).filter(Boolean))
const ipRe = /^\d{1,3}(\.\d{1,3}){3}(\/\d{1,2})?$/
const badIps = computed(() => parsed.value.filter((s) => !ipRe.test(s)))

const okEnabled = computed(() => !!pool.value && parsed.value.length > 0 && badIps.value.length === 0)

async function createAndToast(confirmCollision?: boolean): Promise<void> {
  await groups.createDeviceGroup({
    pool: pool.value,
    name: name.value.trim() || undefined,
    ips: parsed.value,
    confirm: confirmCollision,
  })
  push(`Группа создана: весь трафик ${parsed.value.length === 1 ? 'адреса' : 'адресов'} идёт через пул ${pool.value}. Проверьте на телефоне: внешний IP должен стать IP пула, а панель роутера остаётся доступной.`, false)
  open.value = false
}

async function save(): Promise<void> {
  if (!okEnabled.value || busy.value) return
  busy.value = true
  try {
    try {
      await createAndToast()
    } catch (e) {
      // коллизия адресов (97-док): предупреждение со списком конфликтов
      // и подтверждение - не запрет
      if (e instanceof ApiError && e.status === 409) {
        const ok = await confirm({
          title: 'Адреса уже заняты',
          text: e.message,
          okLabel: 'Всё равно привязать',
        })
        if (ok) await createAndToast(true)
      } else {
        push(e instanceof Error ? e.message : String(e), true)
      }
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <UiDialog
    v-model:open="open"
    title="Устройство целиком через пул"
    :busy="busy"
    width="520px"
  >
    <div class="form">
      <UiSelect
        v-model="pool"
        :options="poolOptions"
        label="Пул (путь трафика)"
      />
      <UiTextarea
        v-model="ips"
        label="IP-адреса устройств"
        :rows="3"
        mono
        placeholder="192.168.1.50&#10;192.168.1.51"
      />
      <UiHint
        v-if="badIps.length"
        :text="'Не похоже на IP/подсеть: ' + badIps.join(', ')"
      />
      <UiInput
        v-model="name"
        label="Имя группы (необязательно)"
        placeholder="Устройство: 192.168.1.50"
      />
      <UiHint text="Весь трафик перечисленных адресов (IPv4) уйдёт в выбранный пул. Доступ к роутеру и его панели сохраняется всегда. Если пул ляжет - у этих устройств пропадёт интернет, пока пул не оживёт. Группа встаёт последней в списке групп и побеждает при пересечении с группами доменов/подсетей." />
    </div>
    <template #footer>
      <UiButton @click="open = false">
        Отмена
      </UiButton>
      <UiButton
        variant="primary"
        :busy="busy"
        :disabled="!okEnabled"
        @click="save"
      >
        Создать
      </UiButton>
    </template>
  </UiDialog>
</template>
