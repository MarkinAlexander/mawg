<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  UiBanner,
  UiButton,
  UiCard,
  UiDialog,
  UiHint,
  UiIcon,
  UiInput,
  UiSelect,
  type UiSelectOption,
  UiSwitch,
} from '../../components/ui'
import { api } from '../../api/client'
import type { IPAllow, LanClient } from '../../api/types'
import { useConfirm } from '../../stores/confirm'
import { useServerSettings } from '../../stores/settings'
import { useStatus } from '../../stores/status'
import { useSystem } from '../../stores/system'
import { useToast } from '../../stores/toast'
import { startRestartWait } from '../restartFlow'
import { ipValid, lanSubnet24, panelTargetOrigin, subnet24 } from './settingsLogic'

// Настройки панели - паритет легаси-карточки «Настройки» (panel.js 2475-3064):
// авторизация (гвард от отсутствующей учётки), сеть (адрес/порт, dirty-
// «Сохранить», перезапуск демона с ожиданием возврата), разрешённые адреса
// (автосохранение каждого изменения, чекер /24, устройства сети, мой адрес,
// автодописывание адреса клиента на бэкенде), RCI-токен (только Keenetic),
// смена пароля, проверка обновлений, выход.
const srv = useServerSettings();
const { state: status } = useStatus();
const { checkUpdate, upd } = useSystem();
const { confirm } = useConfirm();
const { push } = useToast();

const addr = ref('0.0.0.0');
const port = ref('8090');
const authOn = ref(false);
const allow = ref<IPAllow[]>([]);
const newAllowed = ref('');
const pristine = ref<{ listenAddr: string; port: number; authOn: boolean } | null>(null);
const needRestart = ref(false);
const clients = ref<LanClient[]>([]);
const devSel = ref('');

function applyServer(): void {
  const d = srv.state.data;
  if (!d) return;
  addr.value = d.listenAddr || '0.0.0.0';
  port.value = String(d.port || 8090);
  authOn.value = d.authEnabled;
  allow.value = (d.allowedIps || []).map((e) => ({ v: e.v, on: e.on !== false }));
  pristine.value = { listenAddr: d.listenAddr || '0.0.0.0', port: d.port || 8090, authOn: d.authEnabled };
  // рассинхрон: конфиг уже на другом порту/адресе, демон работает на старом
  needRestart.value = !!(
    (d.runningPort && +d.port !== +d.runningPort) ||
    (d.runningAddr && d.listenAddr && d.listenAddr !== d.runningAddr)
  );
}

onMounted(async () => {
  try {
    await srv.load();
  } catch {
    return; // легаси молчит при ошибке загрузки настроек
  }
  applyServer();
  api<{ clients?: LanClient[] }>('GET', '/lan/clients')
    .then((r) => {
      clients.value = r.clients || [];
    })
    .catch(() => {});
});

const dirty = computed(() => {
  if (!pristine.value) return false;
  return (
    (addr.value.trim() || '0.0.0.0') !== pristine.value.listenAddr ||
    +port.value !== pristine.value.port ||
    authOn.value !== pristine.value.authOn
  );
});

const authHint = computed(() =>
  authOn.value
    ? 'Вход по паролю включён. Панель и API закрыты сессией или токеном.'
    : 'ВНИМАНИЕ: авторизация выключена. Панель и API открыты любому, кто достучится до порта. Это небезопасно, если порт доступен из интернета или большой сети. Разрешённые адреса ниже частично компенсируют риск.',
);

// включение авторизации без сохранённой учётки невозможно (легаси 2675)
function onAuthSwitch(v: boolean): void {
  if (v && srv.state.data && !srv.state.data.hasCreds) {
    authOn.value = false;
    push('Нет сохранённой учётки: сначала задайте пароль', true);
    return;
  }
  authOn.value = v;
}

async function save(): Promise<void> {
  const auth = authOn.value;
  // предупреждение - только при выключении РАНЕЕ ВКЛЮЧЁННОЙ; отмена
  // возвращает галку (легаси 2690)
  if (pristine.value?.authOn && !auth) {
    const ok = await confirm({
      title: 'Отключение авторизации',
      text: 'Панель и API будут открыты ЛЮБОМУ, кто достучится до порта. Если порт доступен из интернета или большой сети - это небезопасно. Продолжить?',
      okLabel: 'Отключить',
    });
    if (!ok) {
      authOn.value = true;
      return;
    }
  }
  try {
    const { needRestart: nr } = await srv.save({
      listenAddr: addr.value.trim() || '0.0.0.0',
      port: +port.value,
      allowedIps: allow.value.map((e) => ({ v: e.v, on: e.on })),
      authDisabled: !auth,
    });
    needRestart.value = nr;
    push(nr ? 'Сохранено. Новый адрес/порт вступят в силу после перезапуска демона' : 'Сохранено');
    applyServer();
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
}

// --- разрешённые адреса: автосохранение каждого изменения ---
function addNew(): Promise<void> {
  return allowAdd(newAllowed.value).then(() => {
    newAllowed.value = '';
  });
}

async function allowPersist(): Promise<boolean> {
  try {
    // бэкенд мог дописать наш адрес (новый список его не покрывал) -
    // показываем эффективный список как есть
    allow.value = await srv.saveAllowed(allow.value);
    return true;
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
    return false;
  }
}

async function allowAdd(value: string): Promise<void> {
  const v = value.trim();
  if (!v) return;
  if (!ipValid(v)) {
    push(`"${v}" не похоже на IP или подсеть - проверьте опечатки`, true);
    return;
  }
  if (allow.value.some((e) => e.v === v)) {
    push('Такой адрес уже есть в списке', true);
    return;
  }
  // защита от чужой подсети: сравнение с адресом клиента и записями
  const mine = srv.state.data?.clientIp;
  const known = allow.value.map((e) => subnet24(e.v)).concat(mine ? [subnet24(mine)] : []);
  const sub = subnet24(v);
  if (known.length && !known.includes(sub)) {
    const ok = await confirm({
      title: 'Другая подсеть',
      text: `Адрес ${v} из подсети ${sub}.*, а вы сейчас в ${mine ? subnet24(mine) + '.*' : 'другой подсети'}. Обычно так бывает из-за опечатки. Точно добавить?`,
      okLabel: 'Добавить',
    });
    if (!ok) return;
  }
  allow.value = [...allow.value, { v, on: true }];
  await allowPersist();
}

async function toggleAllow(e: IPAllow, on: boolean): Promise<void> {
  e.on = on;
  await allowPersist();
}
async function removeAllow(e: IPAllow): Promise<void> {
  allow.value = allow.value.filter((x) => x !== e);
  await allowPersist();
}

const lan = computed(() => lanSubnet24(srv.state.data?.clientIp));
const lanOn = computed(() => !!lan.value && allow.value.some((x) => x.v === lan.value && x.on));
const lanLabel = computed(() => (lan.value ? `вся моя подсеть (${lan.value})` : 'вся моя подсеть'));
async function onLanSwitch(v: boolean): Promise<void> {
  if (!lan.value) {
    push('Адрес не определён - подсеть не вычислить', true);
    return;
  }
  if (v) return allowAdd(lan.value);
  allow.value = allow.value.filter((x) => x.v !== lan.value);
  await allowPersist();
}

const devOptions = computed<UiSelectOption[]>(() => [
  { value: '', label: `устройства сети (${clients.value.length})...` },
  ...clients.value.map((c) => ({
    value: c.ip,
    label: `${c.ip}${c.name ? ' - ' + c.name : ''}${c.mac ? ' (' + c.mac + ')' : ''}`,
  })),
]);
async function onDevice(v: string): Promise<void> {
  if (v) await allowAdd(v);
  devSel.value = '';
}
function addMine(): void {
  const ip = srv.state.data?.clientIp;
  if (ip) return void allowAdd(ip);
  push('Адрес не определён', true);
}

// --- перезапуск панели (легаси ssRestart 2772 + waitForPanel 2746) ---
const restart = ref<ReturnType<typeof startRestartWait> | null>(null);
const restarting = computed(() => !!restart.value?.state.running);
onBeforeUnmount(() => restart.value?.stop());

async function restartPanel(): Promise<void> {
  if (restarting.value) return;
  const target = panelTargetOrigin(addr.value, +port.value, {
    protocol: window.location.protocol,
    hostname: window.location.hostname,
    origin: window.location.origin,
  });
  const targetText = target ? target.replace(window.location.protocol + '//', '') : '';
  if (target && target.startsWith(window.location.protocol + '//127.0.0.1') && !window.location.hostname.includes('127.0.0.1')) {
    push('Панель переедет на 127.0.0.1 - с этого компьютера она будет недоступна напрямую (нужен доступ с роутера или прокси)', true);
  }
  const ok = await confirm({
    title: 'Перезапуск панели',
    text: 'Демон mawg будет перезапущен: панель на 10-30 секунд пропадёт, туннели и маршруты не трогаются (конфиги остаются применёнными). Страница сама вернётся, когда панель оживёт. Перезапустить?',
    okLabel: 'Перезапустить',
  });
  if (!ok) return;
  try {
    await api('POST', '/system/restart');
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
    return;
  }
  restart.value = startRestartWait(target, {
    onDone: (ok2) => {
      if (ok2) {
        window.location.href = (target || window.location.origin) + window.location.pathname;
        return;
      }
      restart.value = null;
      push('Панель не вернулась за отведённое время - проверьте её вручную' + (target ? ` по адресу ${targetText}` : ''), true);
    },
  });
}

// --- смена пароля (легаси dlgPass/pwSave 2475-2500) ---
const pwDlg = ref(false);
const pwBusy = ref(false);
const pwOld = ref('');
const pwNew = ref('');
const pwNew2 = ref('');
const pwErr = ref('');

function openPwDialog(): void {
  pwErr.value = '';
  pwOld.value = '';
  pwNew.value = '';
  pwNew2.value = '';
  pwDlg.value = true;
}
async function pwSave(): Promise<void> {
  if (pwBusy.value) return;
  pwErr.value = '';
  if (pwNew.value.length < 8) {
    pwErr.value = 'минимум 8 символов';
    return;
  }
  if (pwNew.value !== pwNew2.value) {
    pwErr.value = 'пароли не совпадают';
    return;
  }
  pwBusy.value = true;
  try {
    await api('POST', '/auth/password', { old: pwOld.value, password: pwNew.value });
    pwDlg.value = false;
    push('пароль изменён');
  } catch (e) {
    pwErr.value = e instanceof Error ? e.message : String(e);
  } finally {
    pwBusy.value = false;
  }
}

// --- проверка обновлений (легаси checkUpdates, ручной режим) ---
const updInfo = ref('');
async function checkUpd(): Promise<void> {
  try {
    const r = await checkUpdate();
    if (r.error) {
      updInfo.value = 'проверка обновления не удалась: ' + r.error;
      push('Проверка обновления: ' + r.error, true);
      return;
    }
    upd.info = r;
    updInfo.value = r.update
      ? `есть обновление: текущая ${r.current}, последняя ${r.latest} (кнопка в шапке или здесь по проверке)`
      : `обновлений нет: текущая ${r.current}, последняя ${r.latest}`;
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
}

async function logout(): Promise<void> {
  try {
    await api('POST', '/auth/logout');
  } catch {
    // выходим в любом случае
  }
  window.location.reload();
}

// --- RCI-токен (только Keenetic; легаси refreshRCIToken 2978) ---
const isKeenetic = computed(() => status.data?.platform === 'keenetic');
const rciConfigured = ref<boolean | null>(null);
const rciToken = ref('');
const rciPlaceholder = computed(() =>
  rciConfigured.value ? 'задан (скрыт) - введите новый для замены' : 'X-NDMA-TKN',
);
// статус приходит глобальным поллингом позже монтирования - ловим момент
watch(
  isKeenetic,
  async (k) => {
    if (!k || rciConfigured.value !== null) return;
    try {
      const r = await api<{ configured: boolean }>('GET', '/rcitoken');
      rciConfigured.value = r.configured;
    } catch {
      // строка токена останется с дефолтным плейсхолдером
    }
  },
  { immediate: true },
);
async function rciSave(): Promise<void> {
  const token = rciToken.value.trim();
  if (!token) return;
  try {
    await api('PUT', '/rcitoken', { token });
    push('Токен сохранен');
    rciToken.value = '';
    rciConfigured.value = true;
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
}
</script>

<template>
  <UiCard title="Настройки панели">
    <div class="sect">
      Авторизация
    </div>
    <UiSwitch
      :model-value="authOn"
      label="вход по паролю"
      tooltip="включить или выключить вход по паролю"
      @update:model-value="onAuthSwitch"
    />
    <UiHint :text="authHint" />

    <div class="sect">
      Сеть
    </div>
    <div class="netrow">
      <UiInput
        v-model="addr"
        label="адрес"
        placeholder="0.0.0.0"
        tooltip="адрес прослушивания"
      />
      <UiInput
        v-model="port"
        label="порт"
        type="number"
      />
    </div>
    <UiHint text="0.0.0.0 - все интерфейсы, 127.0.0.1 - только локально (для nginx-прокси), конкретный IP - один интерфейс. Авторизация и разрешённые адреса применяются сразу, адрес и порт - после перезапуска демона (кнопка появится здесь же)." />
    <div class="row end">
      <UiButton
        :disabled="!dirty"
        @click="save"
      >
        сохранить
      </UiButton>
    </div>
    <div
      v-if="needRestart"
      class="row"
    >
      <UiBanner
        kind="warn"
        text="адрес или порт изменён - нужен перезапуск демона"
      >
        <UiButton
          variant="primary"
          :busy="restarting"
          :disabled="restarting"
          @click="restartPanel"
        >
          {{ restarting ? 'панель перезапускается...' : 'перезапустить панель' }}
        </UiButton>
      </UiBanner>
    </div>

    <div class="sect">
      Разрешённые адреса
    </div>
    <div class="chips">
      <span
        v-if="!allow.length"
        class="empty"
      >список пуст - доступ с любого адреса</span>
      <span
        v-for="e in allow"
        :key="e.v"
        class="chip"
        :class="{ off: !e.on }"
      >
        {{ e.v }}
        <UiSwitch
          :model-value="e.on"
          :tooltip="e.on ? 'выключить запись' : 'включить запись'"
          @update:model-value="(v) => toggleAllow(e, v)"
        />
        <UiButton
          variant="icon"
          tooltip="удалить запись"
          @click="removeAllow(e)"
        >
          <UiIcon
            name="x"
            title="удалить запись"
          />
        </UiButton>
      </span>
    </div>
    <UiInput
      v-model="newAllowed"
      label="добавить адрес"
      placeholder="192.168.0.82 или 192.168.0.0/24"
      @keydown.enter="addNew"
    >
      <template #action>
        <UiButton @click="addNew">
          добавить
        </UiButton>
      </template>
    </UiInput>
    <UiSwitch
      :model-value="lanOn"
      :label="lanLabel"
      tight
      tooltip="добавить /24 подсети, из которой вы открыли панель (например 192.168.0.0/24); снимите галку - запись уберётся"
      @update:model-value="onLanSwitch"
    />
    <UiSelect
      :model-value="devSel"
      :options="devOptions"
      label="устройство из сети"
      @update:model-value="onDevice"
    >
      <template #action>
        <UiButton
          tooltip="добавить адрес, с которого вы открыли панель"
          @click="addMine"
        >
          мой адрес
        </UiButton>
      </template>
    </UiSelect>
    <UiHint text="IP или подсеть, с которых открыт доступ к панели. Пусто - доступ всем. Изменения применяются и сохраняются сразу. При ручном добавлении адреса, который не покрывает ваш текущий, ваш адрес допишется в список автоматически - чтобы доступ к панели не потерялся. Роутер всегда может открыть панель сам." />

    <template v-if="isKeenetic">
      <div class="sect">
        Токен локального API Keenetic
      </div>
      <UiHint text="На прошивках 5.2+ локальный RCI требует токен (NDM-4515): создайте его в веб-интерфейсе Keenetic и вставьте сюда. Без токена mawg работает через CLI медленнее. На 5.1 и старее не нужен." />
      <UiInput
        v-model="rciToken"
        label="RCI-токен"
        :placeholder="rciPlaceholder"
        mono
      >
        <template #action>
          <UiButton
            :disabled="!rciToken.trim()"
            @click="rciSave"
          >
            сохранить
          </UiButton>
        </template>
      </UiInput>
    </template>

    <div class="row sep">
      <UiButton @click="openPwDialog">
        сменить пароль
      </UiButton>
      <UiButton
        tooltip="сравнить версию с последним релизом на GitHub"
        @click="checkUpd"
      >
        проверить обновления
      </UiButton>
      <UiButton @click="logout">
        выйти
      </UiButton>
    </div>
    <UiHint
      v-if="updInfo"
      :text="updInfo"
    />
  </UiCard>

  <UiDialog
    v-model:open="pwDlg"
    title="Смена пароля"
    :busy="pwBusy"
  >
    <UiInput
      v-model="pwOld"
      label="старый пароль"
      type="password"
    />
    <UiInput
      v-model="pwNew"
      label="новый пароль"
      type="password"
      hint="минимум 8 символов"
    />
    <UiInput
      v-model="pwNew2"
      label="повторите новый"
      type="password"
    />
    <div
      v-if="pwErr"
      class="perr"
      role="alert"
    >
      {{ pwErr }}
    </div>
    <template #footer>
      <UiButton @click="pwDlg = false">
        Отмена
      </UiButton>
      <UiButton
        variant="primary"
        :busy="pwBusy"
        @click="pwSave"
      >
        Сохранить
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.sect {
  font-weight: 600;
  font-size: 13px;
  margin: 14px 0 6px;
}
.sect:first-child {
  margin-top: 0;
}
.row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin: 8px 0;
}
.row.end {
  justify-content: flex-end;
}

.row.sep {
  border-top: 1px solid var(--border);
  padding-top: 12px;
  margin-top: 16px;
}
.netrow {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin: 8px 0;
}
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin: 6px 0;
}
.chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border: 1px solid var(--ok);
  border-radius: 14px;
  padding: 2px 6px 2px 10px;
  font-size: 12px;
}
.chip.off {
  border-color: var(--border);
  opacity: 0.5;
}
.empty {
  color: var(--dim);
  font-size: 12px;
}
.perr {
  color: var(--err);
  font-size: 12px;
}
</style>
