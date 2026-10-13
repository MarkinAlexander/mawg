<script setup lang="ts">
// Демо-страница UI-кита (/next/kit, в TABS не входит): визуальная приёмка
// и живая документация на время Фазы 3. Дев-тулинг, не продукт.
import { reactive, ref } from 'vue'
import {
  UiBadge,
  UiBanner,
  UiButton,
  UiCard,
  UiDialog,
  UiField,
  UiIcon,
  UiInput,
  UiSelect,
  UiSwitch,
  UiTable,
  UiTabs,
  UiTextarea,
  UiHint,
  reorder,
  type UiSelectOption,
  type UiTableColumn,
} from '../../components/ui'
import { useConfirm } from '../../stores/confirm'
import { useToast } from '../../stores/toast'

const { push } = useToast()
const { confirm } = useConfirm()

// кнопки
const busyBtn = ref(false)
function spin() {
  busyBtn.value = true
  setTimeout(() => (busyBtn.value = false), 1500)
}

// поля
const name = ref('')
const nameValidator = (v: string) => (v && !/^[a-z][a-z0-9-]*$/.test(v) ? 'строчная латиница, цифры, дефис' : null)
const pass = ref('s3cret')
const rules = ref('8.8.8.8/32\n1.1.1.1/32')
const pool = ref('proton')
const poolOptions: UiSelectOption[] = [
  { value: 'proton', label: 'proton', hint: 'пул по умолчанию, живой', count: 3 },
  { value: 'warp', label: 'warp', hint: 'фоллбек на случай отвала' },
  { value: 'amnezia-free', label: 'amnezia-free', hint: 'один конфиг на устройство' },
]
const dimmed = ref(false)
const guarded = ref(true)

// карточка
const collapsed = ref(false)

// табы
const tab = ref('a')

// таблица с DnD
interface Row {
  id: string
  name: string
  enable: boolean
}
const cols: UiTableColumn[] = [
  { key: 'name', label: 'значение' },
  { key: 'enable', label: '', width: '40px', align: 'right' },
]
const rows = reactive<Row[]>([
  { id: '1', name: '8.8.8.8/32', enable: true },
  { id: '2', name: 'telegram-subnets', enable: true },
  { id: '3', name: 'youtube', enable: false },
])
function onReorder(from: number, to: number) {
  const next = reorder(rows, from, to)
  rows.splice(0, rows.length, ...next)
}

// диалог
const dlgOpen = ref(false)
const dlgBusy = ref(false)
function dlgSave() {
  dlgBusy.value = true
  setTimeout(() => {
    dlgBusy.value = false
    dlgOpen.value = false
    push('диалог: сохранено')
  }, 1200)
}
</script>

<template>
  <div class="kit">
    <h1>UI-кит — демо</h1>
    <UiHint text="Страница-приёмка компонентов кита (Фаза 2, 44-дока §4). В навигацию не входит." />

    <UiCard
      title="Кнопки"
      subtitle="ghost / primary / danger / icon; busy и disabled"
    >
      <div class="row">
        <UiButton @click="push('клик: обычная')">
          обычная
        </UiButton>
        <UiButton
          variant="primary"
          tooltip="основное действие"
        >
          основная
        </UiButton>
        <UiButton
          variant="danger"
          tooltip="удаление"
        >
          опасная
        </UiButton>
        <UiButton
          variant="icon"
          tooltip="удалить"
        >
          <UiIcon name="trash" />
        </UiButton>
        <UiButton
          variant="icon"
          tooltip="редактировать"
        >
          <UiIcon name="pencil" />
        </UiButton>
        <UiButton
          :busy="busyBtn"
          @click="spin"
        >
          занят 1.5с
        </UiButton>
        <UiButton
          disabled
          tooltip="недоступно"
        >
          недоступная
        </UiButton>
      </div>
    </UiCard>

    <UiCard title="Бейджи и баннеры">
      <div class="row">
        <UiBadge
          text="работает"
          variant="ok"
        />
        <UiBadge
          text="фоллбек"
          variant="err"
          tooltip="пул отвечает ошибками"
        />
        <UiBadge
          text="cooldown 2м"
          variant="warn"
        />
        <UiBadge
          text="новое"
          variant="info"
        />
        <UiBadge
          text="серый"
          variant="muted"
        />
      </div>
      <UiBanner
        kind="info"
        text="Доступно обновление панели: v0.6.12."
        :link="{ href: 'https://github.com/MarkinAlexander/mawg/releases', label: 'что нового' }"
      />
      <UiBanner
        kind="warn"
        text="Подключённый MagiTrickle не поддерживает источники групп: нужен форк rev3+."
      />
      <UiBanner
        kind="err"
        text="Обнаружена глючная сборка MagiTrickle: известно, ломает хуки."
      />
    </UiCard>

    <UiCard
      title="Поля"
      subtitle="UiField внутри: подпись + серый hint + ошибка валидатора"
    >
      <UiInput
        v-model="name"
        label="Имя пула"
        hint="латиница, цифры, дефисы; начинается с буквы"
        placeholder="proton"
        required
        :validator="nameValidator"
      />
      <UiInput
        v-model="pass"
        label="Пароль"
        type="password"
        mono
        hint="глаз показывает символы"
      />
      <UiTextarea
        v-model="rules"
        label="Правила"
        hint="по одному на строку"
        mono
        :rows="3"
      />
      <UiSelect
        v-model="pool"
        :options="poolOptions"
        label="Пул"
        hint="куда отправлять трафик группы"
      />
      <UiSwitch
        v-model="dimmed"
        label="Приглушить правило"
        hint="демо-состояние выключенного тумблера"
      />
      <UiSwitch
        v-model="guarded"
        label="Защитное окно мутаций"
        tooltip="SyncHooks молчит 30с вокруг записи"
        busy
      />
      <UiField
        label="Голый UiField"
        hint="слот под произвольный контрол"
        tooltip="для нестандартных контролов"
        required
      >
        <template #default="{ id }">
          <input
            :id="id"
            placeholder="свой контрол"
          >
        </template>
      </UiField>
    </UiCard>

    <UiCard title="Карточка и табы">
      <UiTabs
        v-model="tab"
        :tabs="[{ id: 'a', label: 'Разное', hint: 'первая вкладка' }, { id: 'b', label: 'Прочее' }]"
      />
      <p v-if="tab === 'a'">
        Содержимое вкладки «Разное».
      </p>
      <p v-else>
        Содержимое вкладки «Прочее».
      </p>

      <UiCard
        v-model:collapsed="collapsed"
        title="Вложенная карточка"
        subtitle="collapsible + actions"
        collapsible
      >
        <template #actions>
          <UiBadge
            text="действие"
            variant="info"
          />
        </template>
        Тело свернул/развернул стрелкой.
      </UiCard>
    </UiCard>

    <UiCard
      title="Таблица с DnD"
      subtitle="грип взводит перетаскивание; половина высоты = до/после"
    >
      <UiTable
        :columns="cols"
        :rows="rows"
        :row-key="(r: Row) => r.id"
        dnd
        :row-class="(r: Row) => (r.enable ? '' : 'rowoff')"
        @reorder="onReorder"
      >
        <template #cell-enable="{ row }">
          <UiIcon :name="row.enable ? 'gear' : 'x'" />
        </template>
      </UiTable>
    </UiCard>

    <UiCard title="Диалоги и тосты">
      <div class="row">
        <UiButton
          variant="primary"
          @click="dlgOpen = true"
        >
          открыть диалог
        </UiButton>
        <UiButton @click="confirm({ title: 'Подтверждение', text: 'Удалить группу «тест» со всеми правилами?', okLabel: 'Удалить' }).then((ok) => push(ok ? 'confirm: да' : 'confirm: нет'))">
          confirm()
        </UiButton>
        <UiButton @click="push('тост: всё хорошо')">
          тост ok
        </UiButton>
        <UiButton @click="push('тост: ошибка', true)">
          тост err
        </UiButton>
      </div>

      <UiDialog
        v-model:open="dlgOpen"
        title="Демо-диалог"
        :busy="dlgBusy"
      >
        <UiField
          label="Что-нибудь"
          hint="внутри диалога busy гасит контент"
        >
          <template #default="{ id }">
            <input :id="id">
          </template>
        </UiField>
        <template #footer>
          <UiButton
            :disabled="dlgBusy"
            @click="dlgOpen = false"
          >
            Отмена
          </UiButton>
          <UiButton
            variant="primary"
            :busy="dlgBusy"
            @click="dlgSave"
          >
            Сохранить
          </UiButton>
        </template>
      </UiDialog>
    </UiCard>
    <!-- ToastHost/ConfirmHost монтируются в NextLayout, здесь их не дублируем -->
  </div>
</template>

<style scoped>
.kit h1 {
  font-size: 18px;
}
.row {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  align-items: center;
  margin: 8px 0;
}
.rowoff {
  opacity: 0.45;
}
</style>
