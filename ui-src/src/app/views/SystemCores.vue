<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiBadge, UiBanner, UiButton, UiHelp, UiHint, UiSelect, UiSwitch } from '../../components/ui'
import { useStatus } from '../../stores/status'
import { useSystem } from '../../stores/system'
import { useConfirm } from '../../stores/confirm'
import { useToast } from '../../stores/toast'
import type { CoreInfo } from '../../api/types'
import { SB_CACHE_TEXT, SB_MODE_OPTIONS as sbModeOptions, coreStateBadge, engineInfoOf, sbCacheWarnOf } from './systemLogic'
import SystemDownloads from './SystemDownloads.vue'
import CoreUploadDialog from './CoreUploadDialog.vue'

// Секция «Ядра VPN» карточки «Система»: lifecycle ядер sing-box/mihomo,
// режим движка и кэш-предупреждение общего ядра. Установка из релиза
// делит busy-поток с таблицей зависимостей: busyId/runInstall приходят
// пропсами из SystemView (диалог хода и результат - там же).
const props = defineProps<{
  busyId: string;
  runInstall: (payload: { id: string; flavor?: string; source?: string; backup?: boolean; replace?: boolean }) => Promise<void>;
}>();

const sys = useSystem();
const { state: status, refresh: refreshStatus } = useStatus();
const { confirm } = useConfirm();
const { push } = useToast();

// заливка/скачивание бинарей движков (112-док 4.1): две кнопки на всё
const uploadOpen = ref(false);
const dlOpen = ref(false);
function openDownloads(): void {
  dlOpen.value = true; // список подтянет SystemDownloads при открытии
}

const sbMode = ref('own');
watch(
  () => status.data?.engine?.mode,
  (m) => {
    if (m) sbMode.value = m;
  },
  { immediate: true },
);
const engine = computed(() => status.data?.engine);
const engineInfo = computed(() => engineInfoOf(engine.value));
const sbApplying = ref(false);
async function applySbMode(): Promise<void> {
  const mode = sbMode.value;
  const ok = await confirm({
    title: 'Сменить режим движка',
    text: mode === 'shared'
      ? 'Пулы mawg переедут в общий конфиг sing-box (mawg-pools.json в каталоге основного ядра). Ваш sing-box будет перезапущен - соединения коротко прервутся.'
      : 'Вернуть отдельный экземпляр движка mawg. Ваш sing-box будет перезапущен без фрагмента mawg.',
    okLabel: 'Сменить',
  });
  if (!ok) {
    sbMode.value = engine.value?.mode || 'own';
    return;
  }
  sbApplying.value = true;
  try {
    const r = await sys.setSingboxMode(mode);
    push(r.warning ? 'Режим сменён, но: ' + r.warning : 'Режим движка: ' + (r.mode || mode), !!r.warning);
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  sbApplying.value = false;
  setTimeout(() => void refreshStatus(), 500);
}
const sbCacheWarn = computed(() => sbCacheWarnOf(engine.value));
const coreBusy = ref('');
async function corePower(c: CoreInfo, action: 'start' | 'stop' | 'enable' | 'disable'): Promise<void> {
  if (action === 'stop') {
    const ok = await confirm({
      title: 'Остановить ' + c.title,
      text: 'Ядро выключается совсем: движковые пулы (tun) перестанут работать, пока вы не включите его обратно. Нативные пулы не затрагиваются.',
      okLabel: 'Остановить',
    });
    if (!ok) return;
  }
  coreBusy.value = action;
  try {
    await sys.corePower(c.id, action);
    push(action === 'start' ? 'Ядро включено, конфиг применён' : action === 'stop' ? 'Ядро остановлено' : 'Автозагрузка сервиса sing-box: ' + (action === 'enable' ? 'включена' : 'выключена'));
    void refreshStatus();
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  coreBusy.value = '';
}
async function coreUninstall(c: CoreInfo): Promise<void> {
  const ok = await confirm({
    title: 'Удалить ' + c.title,
    text: 'Ядро удаляется полностью (маркер mawg). Если при установке сохранялся ваш прежний бинарь - он вернётся на место. Движковые пулы этого ядра перестанут работать, пока ядро не установить снова.',
    okLabel: 'Удалить',
  });
  if (!ok) return;
  coreBusy.value = 'uninstall';
  try {
    await sys.corePower(c.id, 'uninstall');
    push('Ядро удалено');
    void refreshStatus();
    void sys.refresh();
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  coreBusy.value = '';
}
// установка ядра из релиза (88-док, mihomo): confirm -> busy-диалог -> результат
async function coreInstall(c: CoreInfo): Promise<void> {
  if (props.busyId) return;
  const ok = await confirm({
    title: 'Установить ' + c.title,
    text: 'Ядро скачивается из релиза на GitHub (gz-бинарь, до пары минут), проверяется контрольная сумма и тест-запуск. Если в системе уже стоит чужой mihomo - он будет сохранён рядом как .pre-mawg.',
    okLabel: 'Установить',
  });
  if (!ok) return;
  await props.runInstall({ id: c.id });
}

async function cacheAct(mode: 'off' | 'tmp'): Promise<void> {
  const texts = {
    off: ['Выключить кэш ядра', 'Кэш будет выключен (experimental.cache_file.enabled = false): запись на флешку прекратится совсем. Ваш sing-box будет перезапущен - соединения коротко прервутся. Запомненные выборы селекторов сбросятся - urltest-пулы восстановятся сами.', 'Выключить'],
    tmp: ['Перенести кэш ядра в /tmp', 'cache_file переедет в /tmp/sing-box/cache.db: на флешку писаться перестанет, выборы живут до ребута роутера. Ваш sing-box будет перезапущен - соединения коротко прервутся.', 'Перенести'],
  }[mode];
  const ok = await confirm({ title: texts[0], text: texts[1], okLabel: texts[2] });
  if (!ok) return;
  try {
    await sys.setSingboxCache(mode);
    push(mode === 'off' ? 'Кэш ядра выключен - запись на флешку прекращена' : 'Кэш перенесён в /tmp');
    void refreshStatus();
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
}
</script>

<template>
  <div class="sect coresect">
    <span>Ядра VPN<UiHelp text="Жизненный цикл туннельных движков: включить/выключить совсем, автозагрузка сервиса, удаление. Ядра: sing-box и mihomo (Clash.Meta) - второе ядро для движковых пулов, ставится кнопкой «Установить». Остановка выключает ядро целиком: его движковые пулы перестают работать, нативные (WireGuard-слоты) и пулы другого ядра не затрагиваются." /></span>
    <!-- две кнопки на всё (слово владельца 04.11): заменить ядро sing-box/
         mihomo или пакет MT можно и без GitHub - скачать где есть доступ,
         залить здесь, панель сама поймёт что это -->
    <span class="coresect-btns">
      <UiButton
        tooltip="прямые ссылки на файлы под архитектуру этого роутера: sing-box, mihomo, пакет MagiTrickle - если с роутера GitHub недоступен, скачайте где есть доступ"
        @click="openDownloads"
      >
        Скачать
      </UiButton>
      <UiButton
        variant="primary"
        tooltip="залить скачанный бинарь sing-box/mihomo или пакет MagiTrickle (.ipk/.apk): панель сама распознает файл, покажет что нашла и заменит по подтверждению"
        @click="uploadOpen = true"
      >
        Загрузить
      </UiButton>
    </span>
  </div>
  <div
    v-for="c in sys.cores.list"
    :key="c.id"
    class="corerow"
  >
    <div class="coreline">
      <b>{{ c.title }}</b>
      <template v-if="c.installed">
        <UiBadge
          :text="c.version || '?'"
          variant="info"
        />
        <UiBadge
          v-if="c.lx"
          text="lx"
          variant="ok"
        />
        <UiBadge
          v-if="c.extended"
          text="extended"
          variant="ok"
          tooltip="форк sing-box-extended (shtorm-7): xhttp, vless-шифрование и AWG доступны"
        />
        <UiBadge
          :text="c.mode || '?'"
          variant="muted"
        />
        <UiBadge
          :text="coreStateBadge(c).text"
          :variant="coreStateBadge(c).variant"
        />
        <UiBadge
          v-if="c.mode === 'shared'"
          :text="c.autostart ? 'автозагрузка вкл' : 'автозагрузка выкл'"
          :variant="c.autostart ? 'ok' : 'warn'"
          tooltip="автозапуск сервиса sing-box при загрузке роутера"
        />
        <span
          v-if="c.autostartNote"
          class="corenote"
        >{{ c.autostartNote }}</span>
      </template>
      <UiBadge
        v-else
        text="не установлен"
        variant="muted"
      />
    </div>
    <div
      v-if="c.lastError && !c.running"
      class="coreerr"
    >
      не запущен: {{ c.lastError }}
    </div>
    <div
      v-if="c.installed"
      class="coreact"
    >
      <UiButton
        v-if="c.caps.start"
        variant="primary"
        :busy="coreBusy === 'start'"
        @click="corePower(c, 'start')"
      >
        Запустить
      </UiButton>
      <UiButton
        v-if="c.caps.stop"
        :busy="coreBusy === 'stop'"
        @click="corePower(c, 'stop')"
      >
        Остановить
      </UiButton>
      <UiSwitch
        v-if="c.mode === 'shared'"
        :model-value="c.autostart"
        :busy="coreBusy === 'enable' || coreBusy === 'disable'"
        label="автозагрузка"
        @update:model-value="(v: boolean) => corePower(c, v ? 'enable' : 'disable')"
      />
      <UiButton
        v-if="c.caps.uninstall"
        variant="danger"
        :busy="coreBusy === 'uninstall'"
        @click="coreUninstall(c)"
      >
        Удалить ядро
      </UiButton>
    </div>
    <div
      v-else-if="c.caps.install"
      class="coreact"
    >
      <UiButton
        variant="primary"
        :busy="busyId === c.id"
        @click="coreInstall(c)"
      >
        Установить
      </UiButton>
    </div>
    <UiHint
      v-if="c.note"
      :text="c.note"
    />
  </div>
  <UiHint text="«Свой экземпляр» - панель держит движок сама; «Общее ядро» - пулы живут внутри вашего sing-box, обновляются без разрыва соединений." />
  <UiSelect
    v-model="sbMode"
    :options="sbModeOptions"
    label="режим движка"
  >
    <template #action>
      <UiButton
        variant="primary"
        :busy="sbApplying"
        @click="applySbMode"
      >
        применить
      </UiButton>
    </template>
  </UiSelect>
  <UiHint :text="engineInfo" />
  <UiBanner
    v-if="sbCacheWarn"
    kind="warn"
    :text="SB_CACHE_TEXT"
  >
    <UiButton
      variant="primary"
      @click="cacheAct('off')"
    >
      выключить кэш
    </UiButton>
    <UiButton @click="cacheAct('tmp')">
      в /tmp
    </UiButton>
  </UiBanner>

  <SystemDownloads v-model:open="dlOpen" />
  <CoreUploadDialog v-model:open="uploadOpen" />
</template>

<style scoped>
.sect {
  font-weight: 600;
  font-size: 13px;
  margin: 16px 0 6px;
}
.corerow {
  padding: 8px 0 4px;
  border-bottom: 1px dashed var(--border);
}
.corerow:last-of-type {
  border-bottom: none;
}
.coreline {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}
.coreline > b {
  margin-right: 4px;
}
.corenote {
  color: var(--dim);
  font-size: 12px;
}
.coreerr {
  color: var(--warn, #b8860b);
  font-size: 12px;
  margin-top: 4px;
  word-break: break-word;
}
.coresect {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.coresect-btns {
  display: flex;
  gap: 8px;
}
.coreact {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: center;
  margin-top: 6px;
}
</style>
