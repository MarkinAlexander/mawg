<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiButton, UiDialog, UiHelp, UiHint, UiInput, UiSelect, type UiSelectOption, UiSwitch, UiTextarea } from '../../components/ui'
import { useIfaces } from '../../stores/ifaces'
import { usePoolActions } from '../../stores/pools'
import { useStatus } from '../../stores/status'
import { useCaptcha } from '../../stores/captcha'
import { useConfirm } from '../../stores/confirm'
import { useToast } from '../../stores/toast'
import { useGroups } from '../../stores/groups'
import { ApiError } from '../../api/client'
import type { DeviceBinding, PoolMember, PoolView } from '../../api/types'
import { fallbackHint } from './poolsLogic'
import {
  DEV_IP_RE, amnCountryOptionsOf, coreOptions, fbOptionsOf, groupModeOptions,
  nodeOptionsOf, nodeShortName, premCountryOptionsOf, premNoteOf,
  probeHostFor, probeTypeFor, probeTypeOptions, refreshSourceMsg,
  scDraftOf, scPoolOptionsOf, showAmnCountryOf, smartCascadeBody, splitIps,
} from './poolSettingsLogic'
import { G } from '../glossary'
import PoolMembersSection from './PoolMembersSection.vue'
import PoolExcludesSection from './PoolExcludesSection.vue'
import PoolDevicesSection from './PoolDevicesSection.vue'
import PoolCascadeSection from './PoolCascadeSection.vue'

// Диалог настроек пула (легаси openSettings/spSave 935-1043). Секции
// «Состав», «Исключения», «Устройства», «Умный каскад» - отдельные
// компоненты; чистая логика (опции, дефолты, тела запросов) - в
// poolSettingsLogic.ts
const props = defineProps<{ poolName: string }>();
const open = defineModel<boolean>('open', { default: false });

const { state: status } = useStatus();
const a = usePoolActions();
const { push } = useToast();
const { confirm } = useConfirm();
const { state: ifaces } = useIfaces();

const pool = ref<PoolView | null>(null);
const source = ref('');
const newName = ref('');
const probeType = ref('http');
const probeHost = ref('');
const rttOn = ref(false);
const rtt = ref('300');
const interval = ref('30');
const threshold = ref('3');
const cooldown = ref('5');
const keepalive = ref('25');
const updateInt = ref('0');
const fallback = ref('direct');
// исключения пула (94-док): черновик + пачка через разделители.
// exclInit - что было при открытии: payload несёт excludes ТОЛЬКО когда
// секцию меняли - иначе любое сохранение настроек стирало бы список
// при несросшемся предзаполнении (живой кейс Кинетика 29.10)
const exclList = ref<string[]>([]);
const exclInit = ref('[]');
const exclNew = ref('');
// функционал выключен флагом (95-док, слово владельца 30.10): секция
// гасится плашкой и disabled-контролами; dirty-гейт и так не пустил бы
// excludes в payload, пока секцию не меняли
const excludesDisabled = computed(() => !!status.data?.excludesDisabled);
const exclDirty = computed(() => JSON.stringify(exclList.value) !== exclInit.value);
// устройства через пул (97-док): реестр связок - чипы адресов с ✕
// (снять привязку = удалить MT-группу), добавление сразу в MT (каждая
// операция - запись групп). Коллизия адреса - 409 сервера + подтверждение
// (паттерн петлезащиты DnsView, как в DeviceGroupDialog)
const groups = useGroups();
const devList = computed(() => groups.state.bindings.filter((b) => b.pool === props.poolName));
const devNew = ref('');
const devBusy = ref(false);
function devParse(): string[] {
  const parts = splitIps(devNew.value);
  if (!parts.length) return [];
  const bad = parts.filter((x) => !DEV_IP_RE.test(x));
  if (bad.length) {
    push('Не похоже на IP/подсеть: ' + bad.join(', '), true);
    return [];
  }
  return parts;
}
async function devApplyDraft(): Promise<boolean> {
  const ips = devParse();
  if (!ips.length || devBusy.value) return false;
  devBusy.value = true;
  try {
    try {
      await groups.createDeviceGroup({ pool: props.poolName, ips });
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        const ok = await confirm({ title: 'Адреса уже заняты', text: e.message, okLabel: 'Всё равно привязать' });
        if (!ok) return false;
        await groups.createDeviceGroup({ pool: props.poolName, ips, confirm: true });
      } else {
        push(e instanceof Error ? e.message : String(e), true);
        return false;
      }
    }
    push(`Устройства привязаны: весь трафик ${ips.length === 1 ? 'адреса' : 'адресов'} идёт через пул ${props.poolName}`);
    devNew.value = '';
    return true;
  } finally {
    devBusy.value = false;
  }
}
async function devAdd(): Promise<void> {
  if (!devNew.value.trim()) return;
  await devApplyDraft();
}
async function devRemove(b: DeviceBinding): Promise<void> {
  const ok = await confirm({
    title: 'Снять привязку устройства?',
    text: `Группа «${b.name}» будет удалена: ${b.ips.join(', ')} больше не пойдут через пул ${b.pool} - трафик этих устройств вернётся напрямую.`,
    okLabel: 'Снять привязку',
  });
  if (!ok || devBusy.value) return;
  devBusy.value = true;
  try {
    await groups.deleteDeviceGroup(b.name);
    push('Привязка снята: устройства идут напрямую');
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  } finally {
    devBusy.value = false;
  }
}
// умный каскад (86-док): Via - упорядоченный список (порядок = приоритет)
const scOn = ref(false);
const scVia = ref<string[]>([]);
const scMode = ref('schedule');
const scTrigMode = ref('exhaust');
const scTrigFails = ref('3');
const scInterval = ref('6');
const scTimes = ref('03:00');
const scDwell = ref('10');
const groupMode = ref('urltest');
const rotateSec = ref('300');
const engineNodes = ref<{ tag: string; delay: number; off?: boolean }[]>([]);
const engineNow = ref('');
const members = ref<PoolMember[]>([]);
const freeConf = ref('');
const premCountry = ref('');
const amnCountry = ref('');
const busy = ref(false);

const isEng = computed(() => pool.value?.settings.engineMode === 'singbox');
const isFree = computed(() => !!pool.value?.free);
// ядро движка пула (88-док): выбор при установленном mihomo (кроме Free)
const engineCore = ref('');
const mihomoAvailable = computed(() => !!status.data?.engines?.mihomo?.available);
const showCoreSelect = computed(() => isEng.value && mihomoAvailable.value && !isFree.value);
const isPrem = computed(() => !!(pool.value?.premium || {}).imported);
const sourceRowShown = computed(() =>
  !isFree.value && !isPrem.value && (isEng.value || !!pool.value?.settings.source));
const renameShown = computed(() => status.data?.platform === 'keenetic');
const fbHintText = computed(() => fallbackHint(
  fallback.value,
  ifaces.list.map((i) => ({ device: i.device, probe: i.probe })),
  (dev) => {
    const i = ifaces.list.find((x) => x.device === dev);
    return i?.slot && i.slot !== dev ? `${dev} - ${i.slot}` : dev;
  },
));

const nodeOptions = computed<UiSelectOption[]>(() => nodeOptionsOf(engineNodes.value));
async function pickEngineNode(tag: string): Promise<void> {
  if (!tag || tag === engineNow.value || busy.value) return;
  busy.value = true;
  try {
    await a.engineSelect(props.poolName, tag);
    engineNow.value = tag;
    push('Узел выбран: ' + nodeShortName(tag));
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  } finally {
    busy.value = false;
  }
}
async function loadEngineNodes(): Promise<void> {
  try {
    const r = await a.engine(props.poolName);
    engineNodes.value = r.nodes || [];
    engineNow.value = r.now || '';
  } catch {
    engineNodes.value = [];
    engineNow.value = '';
  }
}
const amnCountryOptions = computed<UiSelectOption[]>(() => amnCountryOptionsOf(pool.value?.settings.amnezia?.countries));
const showAmnCountry = computed(() => showAmnCountryOf(pool.value?.settings.amnezia?.countries));
const premCountryOptions = computed<UiSelectOption[]>(() => premCountryOptionsOf(pool.value?.premium?.countries || []));
const premNote = computed(() => premNoteOf(pool.value?.premium?.pending));
const fbOptions = computed<UiSelectOption[]>(() =>
  fbOptionsOf(status.data?.pools || [], ifaces.list, props.poolName, pool.value?.device));
const probeLocked = computed(() => isFree.value || isEng.value);

const scPoolOptions = computed<UiSelectOption[]>(() => scPoolOptionsOf(status.data?.pools || [], props.poolName));

watch(open, (v) => {
  if (!v) return;
  const p = (status.data?.pools || []).find((x) => x.name === props.poolName);
  if (!p) return;
  pool.value = p;
  members.value = [];
  freeConf.value = '';
  source.value = p.settings.source || '';
  newName.value = p.name;
  probeType.value = probeTypeFor(p.settings, isEng.value);
  probeHost.value = probeHostFor(p.settings, isFree.value);
  rttOn.value = (p.settings.maxRttMs || 0) > 0;
  rtt.value = String((p.settings.maxRttMs || 0) > 0 ? p.settings.maxRttMs : 300);
  interval.value = String(p.settings.checkIntervalSec ?? 30);
  threshold.value = String(p.settings.failThreshold ?? 3);
  cooldown.value = String(p.settings.cooldownMin ?? 5);
  keepalive.value = String(p.settings.keepalive ?? 0);
  updateInt.value = String(p.settings.updateIntervalH || 0);
  const gm = p.settings.groupMode;
  groupMode.value = gm === 'selector' || gm === 'rotate' ? gm : 'urltest';
  engineCore.value = p.settings.engineCore === 'mihomo' ? 'mihomo' : '';
  rotateSec.value = String((p.settings.rotateSec || 0) > 0 ? (p.settings.rotateSec as number) : 300);
  if (isEng.value) void loadEngineNodes();
  const fb = p.settings.fallback || 'direct';
  fallback.value = fbOptions.value.some((o) => o.value === fb) ? fb : 'direct';
  exclList.value = [...(p.settings.excludes || [])];
  exclInit.value = JSON.stringify(exclList.value);
  exclNew.value = '';
  const d = scDraftOf(p.settings.smartCascade);
  scOn.value = d.on;
  scVia.value = d.via;
  scMode.value = d.mode;
  scTrigMode.value = d.trigMode;
  scTrigFails.value = d.trigFails;
  scInterval.value = d.interval;
  scTimes.value = d.times;
  scDwell.value = d.dwell;
  amnCountry.value = p.settings.amnezia?.country || '';
  premCountry.value = p.premium?.country || '';
  void loadMembers();
  void loadFreeConf(p);
  void groups.loadBindings(); // реестр устройств - свежим к открытию
  formInit.value = JSON.stringify(formVals()); // база dirty-гейта «Сохранить»
});

async function loadMembers(): Promise<void> {
  try {
    members.value = await a.members(props.poolName);
  } catch {
    members.value = [];
  }
}
async function loadFreeConf(p: PoolView): Promise<void> {
  const c = (p.configs || [])[0];
  if (!p.free || !c) return;
  try {
    freeConf.value = await a.configText(p.name, c.file);
  } catch {
    freeConf.value = '';
  }
}

async function copyConf(): Promise<void> {
  try {
    await navigator.clipboard.writeText(freeConf.value);
    push('Конфиг скопирован в буфер');
  } catch {
    push('Не удалось скопировать - выделите текст вручную', true);
  }
}

async function doReissue(): Promise<void> {
  const ok = await confirm({
    title: 'Перевыпустить Amnezia Free?',
    text: 'Конфиг привязан к устройству и выдаётся один на идентичность. Перевыпуск сбросит идентичность и запросит новый конфиг (капча - если gateway её запросит). Пул будет пересоздан с теми же настройками и останется выключенным. Если у сервиса один сервер - конфиг вернётся тот же, в сообщении будет видно.',
    okLabel: 'Перевыпустить',
  });
  if (!ok) return;
  await withBusy(async () => {
    try {
      const cap = useCaptcha();
      let plan = await a.reissueFree(props.poolName);
      while (plan && plan.captchaRequired && plan.captcha && plan.captcha.image) {
        // «обновить картинку» в хосте = перезапрос без решения (легаси capRefresh)
        const answer = await cap.ask(
          plan.captcha,
          async () => {
            const p = await a.reissueFree(props.poolName);
            if (!p.captcha) throw new Error('капча не получена');
            return p.captcha;
          },
        );
        if (!answer) return;
        plan = await a.reissueFree(props.poolName, answer);
      }
      if (plan) {
        open.value = false;
        push((plan.warnings || [])[0] || 'Amnezia Free перевыпущен - пул пересоздан, включите его кнопкой', true);
      }
    } catch (e) {
      push(e instanceof Error ? e.message : String(e), true);
    }
  });
}

async function doRefreshSource(): Promise<void> {
  const src = source.value.trim();
  if (!src) {
    push('Вставьте ссылку или URL подписки', true);
    return;
  }
  await withBusy(async () => {
    try {
      const body: { source: string; country?: string } = { source: src };
      if (showAmnCountry.value && amnCountry.value) body.country = amnCountry.value;
      const plan = await a.refreshSource(props.poolName, body);
      push(refreshSourceMsg(plan), !!(plan.warnings && plan.warnings.length));
      open.value = false;
    } catch (e) {
      push(e instanceof Error ? e.message : String(e), true);
    }
  });
}

async function doSwitchCountry(): Promise<void> {
  if (!premCountry.value) {
    push('Выберите страну', true);
    return;
  }
  await withBusy(async () => {
    try {
      await a.switchPremiumCountry(props.poolName, premCountry.value);
      const lbl = premCountryOptions.value.find((o) => o.value === premCountry.value);
      push('Страна применена' + (lbl ? ': ' + lbl.label : ''));
    } catch (e) {
      push(e instanceof Error ? e.message : String(e), true);
    }
  });
}

async function withBusy(fn: () => Promise<void>): Promise<void> {
  if (busy.value) return;
  busy.value = true;
  try {
    await fn();
  } finally {
    busy.value = false;
  }
}

// dirty-гейт «Сохранить» (слово владельца 02.11: «Отмена и Сохранить
// активны без всяких изменений»): снапшот значений, которые реально
// уносит save() (rename, проба, интервалы, фоллбек, excludes, ядро,
// умный каскад). source/страны - не считаются: они применяются своими
// кнопками; привязки устройств применяются сразу - в гейт не входят.
function formVals(): unknown[] {
  return [
    // черновик привязки тоже делает «Сохранить» активной: вписанный IP
    // применяется этим «Сохранить» (молча терять его нельзя)
    devNew.value.trim(),
    newName.value.trim(), probeHost.value.trim(), rttOn.value, rtt.value,
    interval.value, threshold.value, cooldown.value, keepalive.value,
    updateInt.value, fallback.value,
    excludesDisabled.value ? null : exclList.value,
    isEng.value ? engineCore.value : '',
    isEng.value ? [groupMode.value, rotateSec.value] : [],
    isEng.value ? [] : [scOn.value, scVia.value, scTrigMode.value, scTrigFails.value, scInterval.value, scTimes.value, scDwell.value],
  ];
}
const formInit = ref('[]');
const settingsDirty = computed(() => JSON.stringify(formVals()) !== formInit.value);

async function save(): Promise<void> {
  const p = pool.value;
  if (!p) return;
  // черновик привязки из секции устройств: «Сохранить» применяет всё
  // видимое - вписанный IP без «+ привязать» не теряется молча
  if (devNew.value.trim()) {
    const applied = await devApplyDraft();
    if (!applied) {
      push('Привязка устройства не применена: проверьте адрес или подтвердите коллизию. Настройки не сохранены.', true);
      return;
    }
  }
  await withBusy(async () => {
    try {
      const renamed = newName.value.trim();
      const smartBody = isEng.value ? undefined : smartCascadeBody(
        {
          on: scOn.value, via: scVia.value, mode: scMode.value, trigMode: scTrigMode.value,
          trigFails: scTrigFails.value, interval: scInterval.value, times: scTimes.value, dwell: scDwell.value,
        },
        new Set(scPoolOptions.value.map((o) => o.value)),
      );
      await a.update(p.name, {
        probeHost: probeHost.value.trim(),
        checkIntervalSec: +interval.value,
        failThreshold: +threshold.value,
        cooldownMin: +cooldown.value,
        keepalive: +keepalive.value,
        fallback: fallback.value,
        maxRttMs: rttOn.value ? +rtt.value || 0 : 0,
        updateIntervalH: updateInt.value === '' ? 0 : +updateInt.value || 0,
        ...(exclDirty.value ? { excludes: [...exclList.value] } : {}),
        ...(isEng.value ? {
          // ядро шлём всегда явно: «singbox» = вернуть на sing-box
          engineCore: engineCore.value === 'mihomo' ? 'mihomo' : 'singbox',
          groupMode: groupMode.value,
          ...(groupMode.value === 'rotate' ? { rotateSec: Math.max(10, +rotateSec.value || 300) } : {}),
        } : {}),
        ...smartBody,
      });
      if (renamed && renamed !== p.name) await a.rename(p.name, renamed);
      open.value = false;
    } catch (e) {
      push(e instanceof Error ? e.message : String(e), true);
    }
  });
}
</script>

<template>
  <UiDialog
    v-model:open="open"
    :title="'Настройки пула ' + poolName"
    :busy="busy"
    width="520px"
  >
    <template v-if="pool">
      <template v-if="sourceRowShown">
        <div class="sect">
          Источник (ссылка, vpn:// или URL подписки)
        </div>
        <div class="hrow">
          <UiTextarea
            v-model="source"
            label="Источник"
            :rows="3"
            mono
            class="secret"
          />
          <UiHelp :text="G.source" />
        </div>
        <div class="inrow">
          <UiSelect
            v-if="showAmnCountry"
            v-model="amnCountry"
            :options="amnCountryOptions"
            label="локация у gateway Амнезии"
            class="amnsel"
          />
          <UiButton
            :busy="busy"
            tooltip="заново разобрать источник и пересобрать пул (конфиги заменяются)"
            @click="doRefreshSource"
          >
            обновить из источника
          </UiButton>
        </div>
        <UiInput
          v-model="updateInt"
          type="number"
          label="Автообновление источника, ч"
          hint="0 = из заголовка подписки, без него сутки; при деградации пула mawg перепроверит источник и сам"
        />
        <UiHint text="Сервер сменил адрес или ссылка перестала работать - вставьте новую и нажмите. Старые конфиги пула будут заменены разобранными. Для ключа Amnezia mawg повторит обмен (тем же устройством, без новой выдачи); можно сменить локацию списком." />
      </template>

      <UiInput
        v-if="renameShown"
        v-model="newName"
        label="Имя (переименование)"
        tooltip="строчная латиница, цифры и дефисы, начинается с буквы, до 15 символов"
      />

      <template v-if="isFree && freeConf">
        <div class="sect">
          Выданный конфиг Amnezia Free (можно скопировать в официальный клиент)
        </div>
        <pre class="conf">{{ freeConf }}</pre>
        <div class="inrow end">
          <UiButton @click="copyConf">
            Скопировать
          </UiButton>
        </div>
      </template>

      <template v-if="isFree">
        <UiHint text="Amnezia Free: один конфиг на устройство - выдача привязана к идентичности (UUID+ключ), второго конфига для ротации в этом пуле быть не может. «Перевыпустить» = сброс идентичности и новый запрос конфига (капча - если gateway запросит); пул пересоздаётся с теми же настройками и остаётся выключенным. Если у сервиса сервер один - вернётся тот же: endpoint будет виден в сообщении после перевыпуска и в событиях." />
        <div class="inrow end">
          <UiButton
            :busy="busy"
            tooltip="сбросить идентичность устройства и запросить новый конфиг у gateway"
            @click="doReissue"
          >
            Перевыпустить конфиг
          </UiButton>
        </div>
      </template>

      <template v-if="isPrem">
        <div class="sect">
          Amnezia Premium: страна
        </div>
        <UiSelect
          v-model="premCountry"
          :options="premCountryOptions"
          label="страна"
        >
          <template #action>
            <UiButton
              variant="primary"
              :busy="busy"
              @click="doSwitchCountry"
            >
              Сменить страну
            </UiButton>
          </template>
        </UiSelect>
        <UiHint :text="premNote" />
      </template>

      <PoolMembersSection :members="members" />

      <UiHint
        v-if="isFree"
        text="Цель пробы для Amnezia Free зафиксирована (1.1.1.1 - адрес из списка сервиса): Free - сплит-туннель, сервер пускает только к адресам из выданного списка и без IPv6, произвольная цель будет мигать."
      />
      <div class="hrow">
        <UiSelect
          v-model="probeType"
          :options="probeTypeOptions"
          label="Тип пробы через туннель"
          :disabled="probeLocked"
        />
        <UiHelp :text="G.probeHttp204" />
      </div>
      <UiInput
        v-model="probeHost"
        label="Цель пробы"
        placeholder="http://www.gstatic.com/generate_204"
        :disabled="isFree"
      />
      <UiHint
        v-if="isEng"
        text="Проверка автоматическая: mawg получает целевую страницу через узел (строгий HTTP-тест, ping не используется). После порога отказов узел подписки переключается на следующий, неудачный уходит в cooldown."
      />
      <div class="hrow">
        <UiSelect
          v-if="isEng"
          v-model="groupMode"
          :options="groupModeOptions"
          label="Выбор узла в подписке"
        />
        <UiHelp :text="G.urltest" />
      </div>
      <div
        v-if="showCoreSelect"
        class="hrow"
      >
        <UiSelect
          v-model="engineCore"
          :options="coreOptions"
          label="Ядро движка"
          hint="применяется по «Сохранить»: пул пересобирается на выбранном ядре"
        />
        <UiHelp text="Второе ядро mihomo (Clash.Meta). Узлы xhttp и vless-шифрования mihomo не запустит - такие останутся только у sing-box lx." />
      </div>
      <div
        v-if="isEng && groupMode === 'rotate'"
        class="hrow"
      >
        <UiInput
          v-model="rotateSec"
          label="Менять узел каждые, сек"
          tooltip="минимум 10; перед переходом mawg перемеряет группу и уходит только на ответивший узел"
        />
        <UiHelp :text="G.urltest" />
      </div>
      <div
        v-if="isEng && groupMode === 'selector' && nodeOptions.length"
        class="hrow"
      >
        <UiSelect
          :model-value="engineNow"
          :options="nodeOptions"
          label="Активный узел"
          hint="применяется сразу, без «Сохранить»"
          @update:model-value="pickEngineNode"
        />
        <UiHelp :text="G.urltest" />
      </div>
      <UiHint
        v-if="isEng"
        text="лучший пинг: движок сам держит узел с лучшим пингом (переключает при разнице больше 10 мс). ручной выбор: узел задаётся здесь или молнией в карточке пула. ротация по таймеру: mawg меняет узел по кругу, перемеряя группу и переходя только на рабочие."
      />
      <div class="hrow">
        <UiSwitch
          v-model="rttOn"
          label="считать нерабочим при RTT выше"
          tooltip="включить порог RTT"
        />
        <UiHelp :text="G.rtt" />
      </div>
      <UiInput
        v-model="rtt"
        type="number"
        label="порог RTT, мс"
        :disabled="!rttOn"
      />
      <UiInput
        v-model="interval"
        type="number"
        label="Интервал проверки, сек"
      />
      <div class="hrow">
        <UiInput
          v-model="threshold"
          type="number"
          label="Отказов подряд до ротации"
        />
        <UiHelp :text="G.rotation" />
      </div>
      <div class="hrow">
        <UiInput
          v-model="cooldown"
          type="number"
          label="Cooldown неудачного конфига, мин"
        />
        <UiHelp :text="G.cooldown" />
      </div>
      <div
        v-if="!isEng"
        class="hrow"
      >
        <UiInput
          v-model="keepalive"
          type="number"
          label="Keepalive, сек (0 = из конфига)"
        />
        <UiHelp :text="G.keepalive" />
      </div>
      <div class="hrow">
        <UiSelect
          v-model="fallback"
          :options="fbOptions"
          label="Если все конфиги недоступны"
        />
        <UiHelp :text="G.fallback" />
      </div>
      <UiHint
        v-if="fbHintText"
        :text="fbHintText"
      />
      <PoolExcludesSection
        v-model:list="exclList"
        v-model:draft="exclNew"
        :disabled="excludesDisabled"
      />
      <PoolDevicesSection
        v-model:draft="devNew"
        :list="devList"
        :busy="devBusy"
        @add="devAdd"
        @remove="devRemove"
      />
      <PoolCascadeSection
        v-if="!isEng"
        v-model:on="scOn"
        v-model:via="scVia"
        v-model:trig-mode="scTrigMode"
        v-model:trig-fails="scTrigFails"
        v-model:mode="scMode"
        v-model:interval="scInterval"
        v-model:times="scTimes"
        v-model:dwell="scDwell"
        :pool-options="scPoolOptions"
      />
    </template>
    <template #footer>
      <UiButton @click="open = false">
        Отмена
      </UiButton>
      <UiButton
        variant="primary"
        :busy="busy"
        :disabled="!settingsDirty"
        :tooltip="settingsDirty ? '' : 'изменений нет - привязки устройств применяются сразу, остальное сохраняется этой кнопкой'"
        @click="save"
      >
        Сохранить
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.hrow {
  display: flex;
  gap: 6px;
  align-items: flex-start;
  margin-top: 4px;
}
.hrow > :first-child {
  flex: 1;
  min-width: 0;
}
.sect {
  font-weight: 600;
  font-size: 13px;
  margin: 12px 0 4px;
}
.inrow {
  display: flex;
  gap: 8px;
  align-items: center;
  margin: 4px 0;
}
.inrow.end {
  justify-content: flex-end;
}
.amnsel {
  flex: 1;
}
.conf {
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 8px;
  font: 11px/1.5 ui-monospace, monospace;
  margin: 0;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 220px;
  overflow: auto;
}
</style>
