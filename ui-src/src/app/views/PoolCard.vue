<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { UiBadge, UiBanner, UiBusyOverlay, UiButton, UiCard, UiHelp, UiHint, UiIcon, UiInput, UiSelect, type UiSelectOption, UiSwitch } from '../../components/ui'
import { useConfirm } from '../../stores/confirm'
import { useGroups } from '../../stores/groups'
import { useStatus } from '../../stores/status'
import { usePoolActions, type PoolAct, type PoolRefs } from '../../stores/pools'
import { useToast } from '../../stores/toast'
import { useWarp } from '../../stores/warp'
import { useSecrets } from '../secrets'
import { G } from '../glossary'
import { probeHuman } from './groupsLogic'
import { agoText, isWarpPool } from './poolsLogic'
import SmartCascadeChip from './SmartCascadeChip.vue'
import DeviceGroupDialog from './DeviceGroupDialog.vue'
import LxInstallDialog from './LxInstallDialog.vue'
import PoolNodesSection from './PoolNodesSection.vue'
import PoolConfigsSection from './PoolConfigsSection.vue'
import type { PoolView, TunInfo } from '../../api/types'

// Карточка пула (легаси ensureCard/updateCard 67-265): данные из /status
// (5с), карточка чисто отображает + мутации. Шестерёнка настроек и
// «+ интерфейс» - Ф3.5б (диалоги).
const props = defineProps<{ pool: PoolView }>();
defineEmits<{ settings: [] }>();

const { state: status, refresh: refreshStatus } = useStatus();
const { confirm, confirmChoice } = useConfirm();
const { push } = useToast();
const a = usePoolActions();
// приватный режим: скрываем адреса из конфигов, а не все места подряд
const secrets = useSecrets();

const busy = ref(''); // «name:act» - одна busy-кнопка за раз (легаси withBusy)
// оверлей «идёт применение» на всю карточку (118, слово владельца
// 14.11: операция может висеть секундами - пересборка движка, проверка,
// генерация WARP - надо видеть, что она жива)
const cardBusyText = computed(() => {
  const k = busy.value.slice(props.pool.name.length + 1);
  if (k === 'check') return 'идёт проверка соединения…';
  if (k === 'rotate') return 'смена конфига…';
  if (k === 'warpgen') return 'генерация конфига WARP…';
  if (k === 'del') return 'удаляю пул…';
  return 'идёт применение настроек…';
});
// исключения (94-док): пуловые - из настроек пула, общие - из глобального
// списка (действуют на все пулы); на карточке видно объединение
const groups = useGroups();
onMounted(() => {
  if (!groups.state.excludesLoaded) void groups.loadExcludes();
  if (!groups.state.bindingsLoaded) void groups.loadBindings(); // 97-док
});
const exclOwn = computed(() => props.pool.settings.excludes || []);
const exclGlobal = computed(() => (groups.state.excludes || []).filter((x) => !exclOwn.value.includes(x)));
const exclRow = computed(() => {
  const parts: string[] = [];
  if (exclOwn.value.length) parts.push(exclOwn.value.join(', '));
  if (exclGlobal.value.length) parts.push('общие: ' + exclGlobal.value.join(', '));
  return parts.join(' + ');
});
// устройства через пул (97-док): связки этого пула из реестра -
// строка «устройства» по образцу строки «исключения» (4b1695f)
const devRow = computed(() =>
  (groups.state.bindings || []).filter((b) => b.pool === props.pool.name));
const devRowText = computed(() =>
  devRow.value.map((b) => b.ips.join(', ')).join(', '));
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

// кто использует пул (14.11): наборы-участники, зоны с путём, фоллбеки
// других пулов - предупреждение ДО удаления; аудит подсвечивает мёртвые
// ссылки постфактум, а здесь владелец ещё может передумать. Эндпоинт
// недоступен - удаляем как раньше, без варнинга
async function refsWarning(): Promise<string> {
  let refs: PoolRefs;
  try {
    refs = await a.references(props.pool.name);
  } catch {
    return '';
  }
  const parts: string[] = [];
  if (refs.bundles.length) parts.push('набор' + (refs.bundles.length > 1 ? 'ы' : '') + ' ' + refs.bundles.join(', '));
  if (refs.zones.length) parts.push((refs.zones.length > 1 ? 'зоны ' : 'зона ') + refs.zones.join(', '));
  if (refs.pools.length) parts.push('фоллбек ' + (refs.pools.length > 1 ? 'пулов: ' : 'пула ') + refs.pools.join(', '));
  if (!parts.length) return '';
  return `\n\nПул используют: ${parts.join('; ')}. После удаления эти ссылки станут мёртвыми - панель их подсвечивает.`;
}

async function delPool(): Promise<void> {
  const refs = await refsWarning();
  // есть устройства через пул - выбор судьбы их групп (слово владельца
  // 30.10: «удаление пула должно удалять группу, а если пользователь не
  // удаляет - перевести на директ»: суть mawg - не лишать интернета)
  if (devRow.value.length) {
    const choice = await confirmChoice({
      title: 'Удаление пула с устройствами',
      text: `Удалить пул ${props.pool.name}? Через него ходят устройства: ${devRowText.value}.\n\n«Удалить пул и устройства» - их группы удалятся вместе с пулом.\n«Оставить напрямую» - группы останутся, но выключатся: устройства пойдут напрямую, без интернета никто не останется.${refs}`,
      okLabel: 'Удалить пул и устройства',
      altLabel: 'Оставить напрямую',
    });
    if (!choice) return;
    void run(`${props.pool.name}:del`, () => a.del(props.pool.name, choice === 'ok' ? 'delete' : 'direct'));
    return;
  }
  const ok = await confirm({
    title: 'Удаление пула',
    text: `Удалить пул ${props.pool.name}? Интерфейс будет выключен, конфиги удалены из mawg.${refs}`,
    okLabel: 'Удалить',
  });
  if (!ok) return;
  void run(`${props.pool.name}:del`, () => a.del(props.pool.name));
}

// генератор WARP (77-док): +1 конфиг в живой варп-пул
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

const engine = computed(() => status.data?.engine);
const isEng = computed(() => props.pool.settings.engineMode === 'singbox');

// «Ждёт ядро»: пул жив, но ни один узел не поддерживается установленным
// ядром (112-док 2.3): причина - из матрицы возможностей, в карточке
// варнинг + кнопка поставить подходящее ядро (только у sing-box пулов;
// у mihomo-пула лечится сменой ядра пула в настройках)
const engineWaits = computed<{ detail: string; needsLx: boolean } | null>(() => {
  if (!isEng.value) return null;
  const core = props.pool.settings.engineCore === 'mihomo' ? 'mihomo' : 'singbox';
  const eng = core === 'mihomo' ? status.data?.engines?.mihomo : engine.value;
  const tun = (eng?.tuns || []).find((t: TunInfo) => t.pool === props.pool.name) as
    | (TunInfo & { reason?: string; detail?: string })
    | undefined;
  if (!tun || (tun.reason !== 'waits-lx' && tun.reason !== 'waits-core')) return null;
  return { detail: tun.detail || 'узлы не поддерживаются текущим ядром', needsLx: core === 'singbox' };
});
const lxOpen = ref(false);
// «устройство целиком через пул» (слово владельца 2026-10-28)
const devOpen = ref(false);
const modeBadge = computed(() => {
  if (props.pool.disabled) return { text: 'выключен', variant: 'muted' as const, tooltip: '' };
  if (props.pool.mode === 'fallback') return { text: 'фоллбек', variant: 'err' as const, tooltip: G.fallback };
  return { text: 'работает', variant: 'ok' as const, tooltip: '' };
});

// умный каскад (86-док)
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

// премиум (легаси 209-227): импорт только с nativeAWG3, смена страны -
// только у импортированного
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
</script>

<template>
  <UiCard
    class="busyroot"
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

    <!-- медленная операция - вся карточка затемняется с оверлеем -->
    <UiBusyOverlay
      :show="!!busy"
      :text="cardBusyText"
    />

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
      <!-- мёртвая цель фоллбека (пул удалён/выключен): при отказе группы
           уйдут direct, а не туда, куда думает владелец (аудит 12.11) -->
      <template v-if="pool.fallbackDead">
        <b class="err">фоллбек</b><span class="err">цель не существует: {{ pool.settings.fallback }} - при отказе группы уйдут напрямую</span>
      </template>
      <template v-if="exclRow">
        <b>исключения<UiHelp text="Эти адреса пул не обрабатывает - трафик на них и с них идёт напрямую, мимо туннеля (свои серверы, вторые роутеры). Свои - в настройках пула (шестерёнка), «общие» - в списке исключений на странице правил: они действуют на все пулы." /></b><span>{{ exclRow }}</span>
      </template>
      <template v-if="devRowText">
        <b>устройства<UiHelp :text="G.deviceBinding" /></b><span>{{ devRowText }}</span>
      </template>
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

    <!-- пул ждёт подходящее ядро (112-док 2.3): узлы не поддерживаются
         установленным (PQ REALITY, xhttp, шифрование) - раньше это было
         видно только строчкой при создании пула -->
    <UiBanner
      v-if="engineWaits"
      kind="warn"
      :text="'Пул не запущен: ' + engineWaits.detail"
    >
      <UiButton
        v-if="engineWaits.needsLx"
        variant="primary"
        @click="lxOpen = true"
      >
        Заменить ядро на lx
      </UiButton>
    </UiBanner>

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

    <!-- узлы движкового пула (60-док): режим группы и порядок узлов
         прямо в дровере (118 TASK 0, слово владельца 14.11) -->
    <PoolNodesSection
      v-if="isEng"
      :pool="pool"
    />

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
          tooltip="группа для IP-адресов: весь их трафик идёт через этот пул, доступ к роутеру сохраняется"
          @click="devOpen = true"
        >
          Устройство через пул
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

    <PoolConfigsSection :pool="pool" />
  </UiCard>

  <DeviceGroupDialog
    v-model:open="devOpen"
    :pool-name="pool.name"
  />

  <!-- замена ядра sing-box из варнинга «пул ждёт подходящее ядро» -->
  <LxInstallDialog
    v-model:open="lxOpen"
    @installed="refreshStatus"
  />
</template>

<style scoped>
.card.busyroot {
  position: relative;
}

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
</style>
