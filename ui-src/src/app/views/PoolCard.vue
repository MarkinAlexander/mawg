<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiBadge, UiButton, UiCard, UiHelp, UiHint, UiIcon, UiInput, UiSelect, type UiSelectOption, UiSwitch } from '../../components/ui'
import { useConfirm } from '../../stores/confirm'
import { useStatus } from '../../stores/status'
import { usePoolActions, type PoolAct } from '../../stores/pools'
import { useToast } from '../../stores/toast'
import { useWarp } from '../../stores/warp'
import { useSecrets } from '../secrets'
import { G } from '../glossary'
import { probeHuman } from './groupsLogic'
import { agoText, isWarpPool } from './poolsLogic'
import SmartCascadeChip from './SmartCascadeChip.vue'
import type { ConfigView, PoolEngine, PoolView } from '../../api/types'

// Карточка пула (легаси ensureCard/updateCard 67-265). Данные приходят из
// /status (5с) - карточка чисто отображает + мутации. Шестерёнка настроек и
// «+ интерфейс» - Ф3.5б (диалоги). Вкл/выкл - свитчем в шапке (слово
// владельца: красная кнопка «Выключить» пугала; чекер в едином стиле рядом
// с состоянием). «Сделать активным» конфига - POST /activate (эндпоинт
// был в бэкенде, UI его не выставлял).
const props = defineProps<{ pool: PoolView }>();
defineEmits<{ settings: [] }>();

const { state: status, refresh: refreshStatus } = useStatus();
const { confirm } = useConfirm();
const { push } = useToast();
const a = usePoolActions();
// приватный режим: скрываем адреса из конфигов, а не все места подряд
const secrets = useSecrets();

const busy = ref(''); // «name:act» - одна busy-кнопка за раз (легаси withBusy)
const premiumBusy = ref(false);

async function run(key: string, fn: () => Promise<void>): Promise<void> {
  if (busy.value) return;
  busy.value = key;
  try {
    await fn();
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  busy.value = '';
}

function act(kind: PoolAct): void {
  void run(`${props.pool.name}:${kind}`, () => a.act(props.pool.name, kind));
}

// вкл/выкл пула свитчем в шапке (вместо красной кнопки)
const enabled = computed(() => !props.pool.disabled);
function toggleEnabled(v: boolean): void {
  void run(`${props.pool.name}:${v ? 'enable' : 'disable'}`, () => a.act(props.pool.name, v ? 'enable' : 'disable'));
}

// «Проверить» с обратной связью (слово владельца: «нажимаю и не понимаю
// что происходит»): триггерим пробу и держим кнопку busy, пока /status не
// принесёт новый lastResult (максимум ~15с), вердикт - тостом по-человечески
// словарь вердиктов проверки ([?], 70-док): handshake/icmp/lossy/link down
const probeHelp = 'handshake - сколько секунд назад было «рукопожатие» туннеля (соединение живо); icmp/tcp - проверка пингом сервера; lossy - пинг выше порога (канал жив, но медленный, порог - в настройках пула); «выше порога» - отказ засчитан; link down - интерфейс опущен; settling - конфиг применён, идёт стабилизация';
async function checkNow(): Promise<void> {
  if (probing.value || busy.value) return;
  probing.value = true;
  busy.value = `${props.pool.name}:check`;
  const before = props.pool.lastResult;
  push('Проверка запущена: соединение и пинг сервера…');
  try {
    await a.act(props.pool.name, 'check');
    for (let i = 0; i < 10; i++) {
      await new Promise((r) => setTimeout(r, 1500));
      await refreshStatus();
      if (props.pool.lastResult && props.pool.lastResult !== before) break;
    }
    push('Проверка: ' + probeHuman(props.pool.lastResult));
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  probing.value = false;
  busy.value = '';
}
const probing = ref(false);

async function delPool(): Promise<void> {
  const ok = await confirm({
    title: 'Удаление пула',
    text: `Удалить пул ${props.pool.name}? Интерфейс будет выключен, конфиги удалены из mawg.`,
    okLabel: 'Удалить',
  });
  if (!ok) return;
  void run(`${props.pool.name}:del`, () => a.del(props.pool.name));
}

// --- генератор WARP (77-док): +1 конфиг в живой варп-пул ---
const warpStore = useWarp();
// пул варповый: создан генератором или конфиги смотрят в сети Cloudflare
const isWarp = computed(() => isWarpPool(props.pool));
function genWarp(): void {
  void run(`${props.pool.name}:warpgen`, async () => {
    const plan = await warpStore.addToPool(props.pool.name, {});
    void refreshStatus();
    if (plan.error) {
      push(plan.error, true);
      return;
    }
    const res = plan.results && plan.results[0];
    if (res && res.ok) {
      push(`Конфиг добавлен (${res.endpoint}), проба прошла`);
    } else if (res) {
      push(`Конфиг добавлен (${res.endpoint}), но проба не прошла: ${res.detail || res.error || 'нет ответа'}`, true);
    } else {
      push('Конфиг добавлен');
    }
  });
}

// --- строка карточки (легаси updateCard 174) ---
const engine = computed(() => status.data?.engine);
const isEng = computed(() => props.pool.settings.engineMode === 'singbox');

// --- узлы движкового пула (60-док): список + пинги из clash_api движка,
// ручной выбор в selector (⚡), перемер в urltest. Ленивая загрузка по
// открытию секции - как «Конфиги» у нативных пулов. ---
const nodesOpen = ref(false);
const engineState = ref<PoolEngine | null>(null);
const engineErr = ref('');
async function loadEngine(): Promise<void> {
  engineErr.value = '';
  try {
    engineState.value = await a.engine(props.pool.name);
  } catch (e) {
    engineState.value = null;
    engineErr.value = e instanceof Error ? e.message : String(e);
  }
}
watch(nodesOpen, (v) => {
  if (v) void loadEngine();
});
const engMode = computed(() =>
  engineState.value?.groupMode || props.pool.settings.groupMode || engineState.value?.mode || 'urltest');
async function toggleNode(tag: string, on: boolean): Promise<void> {
  if (busy.value) return;
  await run(`${props.pool.name}:nodeoff`, async () => {
    await a.engineToggle(props.pool.name, tag);
    push(on ? 'Узел исключён из пула' : 'Узел возвращён в пул');
    await loadEngine();
  });
}
async function pickNode(tag: string): Promise<void> {
  if (busy.value) return;
  await run(`${props.pool.name}:selnode`, async () => {
    await a.engineSelect(props.pool.name, tag);
    push(`Выбран узел: ${tag}`);
    await loadEngine();
  });
}
async function retestNodes(): Promise<void> {
  if (busy.value) return;
  await run(`${props.pool.name}:retest`, async () => {
    push('Перемер задержек узлов запущен…');
    await a.engineRetest(props.pool.name);
    await loadEngine();
  });
}
const modeBadge = computed(() => {
  if (props.pool.disabled) return { text: 'выключен', variant: 'muted' as const, tooltip: '' };
  if (props.pool.mode === 'fallback') return { text: 'фоллбек', variant: 'err' as const, tooltip: G.fallback };
  return { text: 'работает', variant: 'ok' as const, tooltip: '' };
});

// --- умный каскад (86-док): бейдж «через Via», план возврата, кнопки ---
const smart = computed(() => props.pool.smart || null);
const smartShown = computed(() =>
  !!(props.pool.settings.smartCascade?.via?.length || smart.value?.on));
const smartNext = computed(() => {
  const t = smart.value?.nextCheck;
  if (!t) return '';
  const d = new Date(t);
  if (Number.isNaN(d.getTime())) return '';
  const hm = d.toTimeString().slice(0, 5);
  return d.toDateString() === new Date().toDateString()
    ? hm
    : `${String(d.getDate()).padStart(2, '0')}.${String(d.getMonth() + 1).padStart(2, '0')} ${hm}`;
});
async function smartAct(kind: 'check-now' | 'on' | 'off'): Promise<void> {
  const key = `${props.pool.name}:smart-${kind}`;
  if (busy.value) return;
  busy.value = key;
  try {
    await a.smartCascade(props.pool.name, kind);
    push(kind === 'on'
      ? 'Каскад включён вручную'
      : kind === 'off'
        ? 'Каскад снят - пул возвращён на прямой путь'
        : 'Проверка прямого пути выполнена');
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  } finally {
    busy.value = '';
  }
}
const sub = computed(() => {
  const p = props.pool;
  if (isEng.value) {
    if (p.disabled) return `${p.settings.tunName} | движок остановлен`;
    // ядро пула (88-док): mihomo-пулы берут состояние своего движка
    const core = p.settings.engineCore === 'mihomo' ? 'mihomo' : 'sing-box';
    const eng = core === 'mihomo' ? status.data?.engines?.mihomo : engine.value;
    return `${p.settings.tunName} | движок ${core}${eng?.version ? ' ' + eng.version : ''}${eng && !eng.running ? ' (не запущен)' : ''}`;
  }
  const slot = p.slot ? `${p.slot} = ` : '';
  if (p.disabled) return `${slot}${p.device} | ротация остановлена`;
  return `${slot}${p.device} | фоллбек: ${p.settings.fallback === 'direct' ? 'напрямую' : 'держать'}`;
});
const result = computed(() => {
  const p = props.pool;
  const when = agoText(p.probeAt);
  return (p.lastResult || '-')
    + (when ? ` · ${when}` : '')
    + (p.consecFails ? ` (отказов подряд: ${p.consecFails}/${p.settings.failThreshold})` : '')
    + (p.refreshFails ? ` ⚠ автообновление источника падало: ${p.refreshFails}` : '');
});

// плашки протоколов движка (что именно гоняет: AWG 3.x, vless/ws, ...)
const protoTitle = (kind: string) => (kind === 'proxy' ? 'узел подписки (vless/trojan и др.)' : 'конфиг WireGuard/AmneziaWG');

// --- премиум (легаси 209-227): импорт только с nativeAWG3, смена страны -
// только у импортированного ---
const premium = computed(() => props.pool.premium || {});
const nativePrem = computed(() => !!status.data?.nativeAWG3);
const premPanelShown = computed(() =>
  !!premium.value.imported
  || (!isEng.value && !(props.pool.configs || []).length && nativePrem.value
    && (props.pool.settings.openwrtProto === 'amneziawg' || props.pool.platform === 'keenetic')));
const premImportShown = computed(() =>
  nativePrem.value && (!premium.value.imported || !(premium.value.countries || []).length));
const premSwitchShown = computed(() => !!premium.value.imported);
const premSwitchDisabled = computed(() =>
  premiumBusy.value || props.pool.disabled || !(premium.value.countries || []).length);
const premNote = computed(() =>
  premium.value.pending
    ? 'Смена не завершена: удалённая регистрация могла измениться. Повторите выбор страны; старый локальный конфиг не гарантирует восстановление.'
    : 'Один слот устройства. Страны не добавляются в пул конфигов.');

const premKey = ref('');
const countryOptions = computed<UiSelectOption[]>(() =>
  (premium.value.countries || []).map((c) => ({ value: c.code, label: `${c.name} (${c.code})` })));
const premCountry = ref('');
// легаси: у импортированного пула страна предвыбрана текущей
watch(
  () => premium.value.country,
  (c) => {
    if (c) premCountry.value = c;
  },
  { immediate: true },
);

async function doImportPremium(): Promise<void> {
  const key = premKey.value.trim();
  premKey.value = '';
  if (!key) {
    push('Введите Premium vpn:// ключ', true);
    return;
  }
  premiumBusy.value = true;
  try {
    await a.importPremium(props.pool.name, key);
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  } finally {
    premiumBusy.value = false;
  }
}
async function doSwitchCountry(): Promise<void> {
  if (!premCountry.value) {
    push('Выберите страну', true);
    return;
  }
  premiumBusy.value = true;
  try {
    await a.switchPremiumCountry(props.pool.name, premCountry.value);
    push('Страна применена');
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  } finally {
    premiumBusy.value = false;
  }
}

// --- конфиги (легаси 230-264 + upload) ---
const cfgOpen = ref(false); // легаси <details> - открытое состояние держится
const fileInput = ref<HTMLInputElement | null>(null);
const dragOver = ref(false);

function cfgRowClass(c: ConfigView): string {
  return c.file === props.pool.activeFile ? 'active' : (c.enabled ? '' : 'dimmed');
}
function pickFiles(): void {
  fileInput.value?.click();
}
function onDrop(e: DragEvent): void {
  dragOver.value = false;
  void doUpload(e.dataTransfer?.files);
}
function onFiles(e: Event): void {
  const inp = e.target as HTMLInputElement;
  void doUpload(inp.files).finally(() => { inp.value = ''; });
}
async function doUpload(files: FileList | null | undefined): Promise<void> {
  if (!files || !files.length) return;
  await run(`${props.pool.name}:upload`, async () => {
    const r = await a.upload(props.pool.name, files);
    let msg = `Добавлено ${r.added}. Дубликаты: ${(r.duplicates || []).length}. Ошибки: ${(r.errors || []).length}`;
    if (r.warnings?.length) msg += '. Конфликты: ' + r.warnings.length;
    push(msg);
    for (const w of r.warnings || []) push(w, true);
  });
}
function cfgAct(key: string, fn: () => Promise<void>): void {
  void run(key, fn);
}
const coolMin = (sec?: number) => Math.ceil((sec || 0) / 60);
</script>

<template>
  <UiCard
    :title="pool.name"
    :subtitle="sub"
  >
    <template #actions>
      <UiBadge
        :text="modeBadge.text"
        :variant="modeBadge.variant"
        :tooltip="modeBadge.tooltip"
      />
      <SmartCascadeChip
        v-if="smartShown && pool.settings.smartCascade"
        :settings="pool.settings.smartCascade"
        :state="smart"
      />
      <!-- вкл/выкл свитчем (слово владельца: единый стиль чекеров, подпись
           состояния рядом; красная кнопка «Выключить» пугала) -->
      <span class="enstate">{{ enabled ? 'включено' : 'отключено' }}</span>
      <UiSwitch
        :model-value="enabled"
        tooltip="включить/выключить пул"
        :disabled="!!busy"
        @update:model-value="toggleEnabled"
      />
      <UiButton
        variant="icon"
        tooltip="настройки пула"
        @click="$emit('settings')"
      >
        <UiIcon name="gear" />
      </UiButton>
      <UiHelp :text="G.pool" />
    </template>

    <div class="kv">
      <!-- secret динамически: только адрес из конфигов; «проверка» и
           счётчики видны всегда (слово владельца 2026-10-15). «конфиг» -
           только нативным пулам: у движкового конфигов нет (слово
           владельца 2026-10-17) -->
      <template v-if="!isEng">
        <b>конфиг<UiHelp :text="G.config" /></b><span :class="{ secret: secrets.endpoint(pool.activeEndpoint) }">{{ pool.activeEndpoint || '-' }}</span>
      </template>
      <b>проверка<UiHelp :text="probeHelp" /></b><span>{{ result }}</span>
      <template v-if="pool.lastError">
        <b class="err">ошибка</b><span
          class="err"
          :class="{ secret: secrets.line(pool.lastError) }"
        >{{ pool.lastError }}</span>
      </template>
      <b>ротаций<UiHelp :text="G.rotation" /></b><span>{{ pool.rotations }}</span>
      <template v-if="smartShown">
        <b>каскад<UiHelp :text="G.smartCascade" /></b><span>
          {{ smart?.on ? `через ${smart.via}` : 'настроен, ждёт блокировки' }}<template v-if="smart?.lastDetail"> — {{ smart.lastDetail }}</template><template v-if="smart?.on && smartNext"> · следующая проверка прямого: {{ smartNext }}</template>
        </span>
      </template>
    </div>
    <UiHint
      v-if="smart?.ensureErr"
      :text="'Служебная группа каскада не создана (' + smart.ensureErr + ') - панель повторит сама, MagiTrickle недоступен?'"
    />

    <div
      v-if="isEng && (pool.protos || []).length"
      class="protos"
    >
      <UiBadge
        v-for="b in pool.protos"
        :key="b.label"
        :text="b.count > 1 ? `${b.label} ×${b.count}` : b.label"
        :tooltip="protoTitle(b.kind)"
      />
    </div>

    <!-- узлы движкового пула: подписка целиком в группе движка (60-док) -->
    <details
      v-if="isEng"
      class="cfg"
      :open="nodesOpen"
      @toggle="nodesOpen = ($event.target as HTMLDetailsElement).open"
    >
      <summary title="узлы подписки в группе движка; urltest держит лучший пинг сам">
        Узлы ({{ engineState ? engineState.nodes.length : '…' }})<UiHelp :text="G.urltest" />
      </summary>
      <UiHint text="Пинг - замер движка до цели пробы, не пинг самого сервера." />
      <UiHint
        v-if="engineErr"
        :text="'ошибка: ' + engineErr"
      />
      <div
        v-if="engineState"
        class="cfglist"
      >
        <div
          v-for="n in engineState.nodes"
          :key="n.tag"
          class="cfgrow"
          :class="{ active: !n.off && engineState.now === n.tag, dimmed: n.off }"
        >
          <span class="cfgmain">
            <span class="cfgname">{{ n.tag }}</span>
            <UiBadge
              v-if="n.type"
              :text="n.type + (n.transport ? '/' + n.transport : '')"
              variant="muted"
              tooltip="протокол узла и его транспорт"
            />
            <UiBadge
              v-if="n.off"
              text="исключён"
              variant="warn"
              tooltip="узел не попадает в конфиг движка; вернуть - тумблером справа"
            />
            <UiBadge
              v-if="!n.off && engineState.now === n.tag"
              text="активен"
              variant="ok"
            />
            <UiBadge
              v-if="!n.off"
              :text="n.delay > 0 ? `${n.delay} мс` : '—'"
              :variant="n.delay > 0 ? 'ok' : 'muted'"
              :tooltip="n.delay > 0 ? 'последний замер движка' : 'не измерен или не отвечает'"
            />
          </span>
          <span class="cfgctl">
            <UiButton
              v-if="engMode === 'selector' && !n.off"
              variant="icon"
              tooltip="выбрать этот узел (selector - ручной режим)"
              :disabled="engineState.now === n.tag"
              :busy="busy === `${pool.name}:selnode`"
              @click="pickNode(n.tag)"
            >
              ⚡
            </UiButton>
            <UiSwitch
              :model-value="!n.off"
              tooltip="участвует в пуле (выключенный узел не попадает в конфиг движка)"
              :disabled="!!busy"
              @update:model-value="(v: boolean) => toggleNode(n.tag, !v)"
            />
          </span>
        </div>
      </div>
      <div
        v-if="engineState"
        class="btnrow"
      >
        <UiButton
          :busy="busy === `${pool.name}:retest`"
          @click="retestNodes"
        >
          Перемерить пинги
        </UiButton>
      </div>
      <UiHint
        v-if="engineState && engMode === 'rotate'"
        :text="'Ротация: mawg меняет узел по кругу каждые ' + (pool.settings.rotateSec || 300) + ' с, перемеряя группу и переходя только на рабочие.'"
      />
    </details>

    <div class="btnrow">
      <template v-if="enabled">
        <UiButton
          v-if="!isEng && !premium.imported"
          :busy="busy === `${pool.name}:rotate`"
          @click="act('rotate')"
        >
          Сменить конфиг
        </UiButton>
        <UiButton
          v-if="!isEng"
          :busy="busy === `${pool.name}:check`"
          @click="checkNow"
        >
          Проверить
        </UiButton>
        <UiButton
          v-if="smartShown && smart?.on"
          :busy="busy === `${pool.name}:smart-check-now`"
          @click="smartAct('check-now')"
        >
          Проверить прямой сейчас
        </UiButton>
        <UiButton
          v-if="smartShown && !smart?.on"
          :busy="busy === `${pool.name}:smart-on`"
          @click="smartAct('on')"
        >
          Завернуть в каскад
        </UiButton>
        <UiButton
          v-if="smart?.on"
          :busy="busy === `${pool.name}:smart-off`"
          @click="smartAct('off')"
        >
          Вернуть напрямую
        </UiButton>
        <UiButton
          v-if="isWarp"
          :busy="busy === `${pool.name}:warpgen`"
          tooltip="ещё одна регистрация WARP с теми же разрешёнными подсетями, новый адрес:порт"
          @click="genWarp"
        >
          Генерация нового конфига
        </UiButton>
      </template>
      <UiButton
        variant="danger"
        :busy="busy === `${pool.name}:del`"
        @click="delPool"
      >
        удалить пул
      </UiButton>
    </div>

    <div
      v-if="premPanelShown"
      class="prem"
    >
      <div
        v-if="premImportShown"
        class="premimport"
      >
        <UiInput
          v-model="premKey"
          type="password"
          label="Amnezia Premium"
          placeholder="vpn://"
        >
          <template #action>
            <UiButton
              :busy="premiumBusy"
              @click="doImportPremium"
            >
              Импортировать
            </UiButton>
          </template>
        </UiInput>
      </div>
      <div
        v-else-if="premSwitchShown"
        class="premswitch"
      >
        <UiSelect
          v-model="premCountry"
          :options="countryOptions"
          label="Страна"
          :disabled="premiumBusy"
        >
          <template #action>
            <UiButton
              :busy="premiumBusy"
              :disabled="premSwitchDisabled"
              @click="doSwitchCountry"
            >
              Сменить страну
            </UiButton>
          </template>
        </UiSelect>
        <UiHint :text="premNote" />
      </div>
    </div>

    <details
      v-if="!isEng"
      class="cfg"
      :open="cfgOpen"
      @toggle="cfgOpen = ($event.target as HTMLDetailsElement).open"
    >
      <summary title="порядок конфигов = приоритет ротации">
        Конфиги ({{ (pool.configs || []).length }})<UiHelp :text="G.rotation" />
      </summary>
      <UiHint text="порядок списка = приоритет ротации, выше = раньше" />
      <div class="cfglist">
        <div
          v-for="(c, i) in pool.configs || []"
          :key="c.file"
          class="cfgrow"
          :class="cfgRowClass(c)"
        >
          <span class="cfgmain">
            <span class="cfgname">{{ c.original }}</span>
            <UiBadge
              v-if="c.file === pool.activeFile"
              text="активен"
              variant="ok"
            />
            <UiBadge
              v-if="(c.coolForSec || 0) > 0"
              :text="`cooldown ${coolMin(c.coolForSec)}м`"
              variant="muted"
              :tooltip="G.cooldown"
            />
            <UiBadge
              v-if="c.conflict"
              text="!"
              variant="warn"
              :tooltip="`${c.conflict}: исключён из ротации, переключатель его не включит`"
            />
            <span
              class="cfgend"
              :class="{ secret: secrets.endpoint(c.endpoint) }"
            >{{ c.endpoint }}</span>
          </span>
          <span class="cfgctl">
            <!-- явный выбор активного конфига (слово владельца): бэкенд-эндпоинт
                 /activate был, UI не выставлял; конфликт адресов валидирует сервер -->
            <!-- скачивание для внешнего клиента - по уровню конфига (слово
                 владельца, третья правка): голый WG - только он сам
                 (ничего не накидываем; AWG-вариант - возможно в будущем,
                 другим цветом с пометкой «наша обфускация»); 2.0 - обе:
                 WG = антиобфускация (снять поля, комментарии остаются),
                 AWG = как есть со своими полями; 3.x - никаких -->
            <a
              v-if="c.awg === 'plain'"
              class="cfgdl"
              :href="`/api/v1/pools/${encodeURIComponent(pool.name)}/configs/${encodeURIComponent(c.file)}`"
              :download="c.file"
              title="голый WireGuard - скачивается как есть"
            >WG</a>
            <template v-if="c.awg === 'awg2'">
              <a
                class="cfgdl"
                :href="`/api/v1/pools/${encodeURIComponent(pool.name)}/configs/${encodeURIComponent(c.file)}?variant=plain`"
                :download="c.file"
                title="антиобфускация: снять поля AmneziaWG (голый WireGuard, комментарии остаются)"
              >WG</a>
              <a
                class="cfgdl"
                :href="`/api/v1/pools/${encodeURIComponent(pool.name)}/configs/${encodeURIComponent(c.file)}`"
                :download="c.file"
                title="скачать как есть (поля обфускации уже в конфиге)"
              >AWG</a>
            </template>
            <UiButton
              variant="icon"
              tooltip="сделать активным: переключить интерфейс на этот конфиг"
              :disabled="c.file === pool.activeFile"
              :busy="busy === `${c.file}:activate`"
              @click="cfgAct(`${c.file}:activate`, async () => {
                await a.activateCfg(pool.name, c.file);
                push(`Конфиг активирован: ${c.original}`);
              })"
            >
              ⚡
            </UiButton>
            <UiButton
              variant="icon"
              tooltip="выше в ротации"
              :disabled="i === 0"
              :busy="busy === `${c.file}:up`"
              @click="cfgAct(`${c.file}:up`, () => a.moveCfg(pool.name, c.file, -1))"
            >
              ↑
            </UiButton>
            <UiButton
              variant="icon"
              tooltip="ниже в ротации"
              :disabled="i === (pool.configs || []).length - 1"
              :busy="busy === `${c.file}:down`"
              @click="cfgAct(`${c.file}:down`, () => a.moveCfg(pool.name, c.file, 1))"
            >
              ↓
            </UiButton>
            <UiSwitch
              :model-value="c.enabled"
              tooltip="участвует в ротации"
              :disabled="!!busy"
              @update:model-value="(v: boolean) => cfgAct(`${c.file}:toggle`, () => a.toggleCfg(pool.name, c.file, v))"
            />
            <UiButton
              variant="icon"
              tooltip="удалить конфиг"
              :busy="busy === `${c.file}:del`"
              @click="cfgAct(`${c.file}:del`, () => a.delCfg(pool.name, c.file))"
            >
              ✕
            </UiButton>
          </span>
        </div>
      </div>
      <div
        v-if="!premium.imported"
        class="drop"
        :class="{ hover: dragOver }"
        @click="pickFiles"
        @dragover.prevent="dragOver = true"
        @dragleave="dragOver = false"
        @drop.prevent="onDrop"
      >
        перетащите .conf / .zip или нажмите
      </div>
      <input
        ref="fileInput"
        type="file"
        multiple
        accept=".conf,.zip"
        style="display: none"
        @change="onFiles"
      >
    </details>
  </UiCard>
</template>

<style scoped>
.actions .enstate {
  color: var(--dim);
  font-size: 11px;
  user-select: none;
}
.kv {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 2px 12px;
  font-size: 13px;
  margin: 8px 0;
}
.kv .err {
  color: var(--err);
}
.protos {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  margin: 4px 0;
}
.btnrow {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  margin: 10px 0 0;
}
.prem {
  margin-top: 10px;
}
.cfg {
  margin-top: 10px;
}
.cfg summary {
  cursor: pointer;
  font-size: 13px;
  font-weight: 600;
}
.cfglist {
  display: grid;
  gap: 2px;
  margin-top: 6px;
}
.cfgrow {
  display: flex;
  align-items: center;
  gap: 10px;
  justify-content: space-between;
  padding: 3px 0;
  border-bottom: 1px solid var(--border);
  font-size: 13px;
}
.cfgrow:last-child {
  border-bottom: 0;
}
.cfgrow.active {
  color: var(--ok);
}
.cfgrow.dimmed {
  opacity: 0.45;
}
.cfgmain {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  flex-wrap: wrap;
}
.cfgname {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cfgend {
  color: var(--dim);
  font-size: 11px;
}
.cfgctl {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex: none;
}
.drop {
  margin-top: 8px;
  border: 1px dashed var(--border);
  border-radius: 8px;
  padding: 14px;
  text-align: center;
  font-size: 12px;
  color: var(--dim);
  cursor: pointer;
}
.drop.hover {
  border-color: var(--accent);
  color: var(--accent);
}
.cfgdl {
  color: var(--accent);
  font: 600 10.5px/1 system-ui, sans-serif;
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 3px 5px;
  text-decoration: none;
  white-space: nowrap;
}
</style>
