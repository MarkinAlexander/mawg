<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiBadge, UiButton, UiDialog, UiHelp, UiHint, UiInput, UiOrderList, UiSelect, type UiSelectOption, UiSwitch, UiTextarea } from '../../components/ui'
import { useIfaces } from '../../stores/ifaces'
import { usePoolActions } from '../../stores/pools'
import { useStatus } from '../../stores/status'
import { useCaptcha } from '../../stores/captcha'
import { useConfirm } from '../../stores/confirm'
import { useToast } from '../../stores/toast'
import type { PoolMember, PoolView } from '../../api/types'
import { fallbackHint } from './poolsLogic'
import { addrSecretIn, hostOf } from '../secrets'
import { G } from '../glossary'

// Диалог настроек пула (легаси openSettings/spSave 935-1043): источник,
// переименование, Free (конфиг/перевыпуск/лок пробы), premium-страна,
// состав пула, проба, интервалы, фоллбек с подсказкой.
const props = defineProps<{ poolName: string }>();
const open = defineModel<boolean>('open', { default: false });

const { state: status } = useStatus();
const a = usePoolActions();

// имя участника скрывается, если в нём сидит хост его же endpoint или
// просто IPv4 (слово владельца 2026-10-17: тег узла выдаёт адрес)
function nameSecret(m: PoolMember): boolean {
  if (!m.name) return false;
  if (addrSecretIn(m.name)) return true;
  return !!m.endpoint && m.name.includes(hostOf(m.endpoint));
}
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
// исключения пула (94-док): черновик + пачка через разделители
const exclList = ref<string[]>([]);
const exclNew = ref('');
function exclAdd(): void {
  const parts = exclNew.value
    .split(/[\s,;]+/)
    .map((x) => x.trim())
    .filter(Boolean);
  for (const x of parts) {
    if (!exclList.value.includes(x)) exclList.value.push(x);
  }
  exclNew.value = '';
}
function exclRemove(i: number): void {
  exclList.value.splice(i, 1);
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
const coreOptions: UiSelectOption[] = [
  { value: '', label: 'sing-box' },
  { value: 'mihomo', label: 'mihomo (Clash.Meta)' },
];
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

const probeTypeOptions: UiSelectOption[] = [
  { value: 'http', label: 'HTTP-страница 204 (надежнее, ICMP режут)' },
  { value: 'icmp', label: 'Ping публичного IP' },
];
const groupModeOptions: UiSelectOption[] = [
  { value: 'urltest', label: 'лучший пинг (авто)' },
  { value: 'selector', label: 'ручной выбор узла' },
  { value: 'rotate', label: 'ротация по таймеру (только рабочие)' },
];
const nodeOptions = computed<UiSelectOption[]>(() =>
  engineNodes.value
    .filter((n) => !n.off)
    .map((n) => {
      const nm = n.tag.includes('|') ? n.tag.slice(n.tag.indexOf('|') + 1) : n.tag;
      return { value: n.tag, label: `${nm}${n.delay > 0 ? ` - ${n.delay} мс` : ''}` };
    }));
async function pickEngineNode(tag: string): Promise<void> {
  if (!tag || tag === engineNow.value || busy.value) return;
  busy.value = true;
  try {
    await a.engineSelect(props.poolName, tag);
    engineNow.value = tag;
    const nm = tag.includes('|') ? tag.slice(tag.indexOf('|') + 1) : tag;
    push('Узел выбран: ' + nm);
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
const amnCountryOptions = computed<UiSelectOption[]>(() =>
  (pool.value?.settings.amnezia?.countries || [])
    .filter((c) => (c.protocols ? c.protocols.length > 0 : true))
    .map((c) => ({
      value: c.code,
      label: c.code + (c.name ? ' - ' + c.name : '') + (c.protocols && c.protocols.length ? ` [${c.protocols.join('/')}]` : ''),
    })));
const showAmnCountry = computed(() => (pool.value?.settings.amnezia?.countries || []).length >= 2);
const premCountryOptions = computed<UiSelectOption[]>(() =>
  (pool.value?.premium?.countries || []).map((c) => ({ value: c.code, label: `${c.name} (${c.code})` })));
const premNote = computed(() =>
  pool.value?.premium?.pending
    ? 'Смена не завершена: регистрация у gateway могла измениться - повторите выбор страны.'
    : 'Один слот устройства. Страны не добавляются в пул конфигов.');
const fbOptions = computed<UiSelectOption[]>(() => {
  const opts: UiSelectOption[] = [
    { value: 'direct', label: 'пустить трафик напрямую' },
    { value: 'hold', label: 'держать последний конфиг' },
  ];
  for (const other of status.data?.pools || []) {
    if (other.name !== props.poolName) {
      opts.push({ value: `pool:${other.name}`, label: `перевести группы на пул ${other.name}` });
    }
  }
  for (const i of ifaces.list) {
    if (i.device === pool.value?.device || i.mode === 'managed') continue;
    opts.push({ value: `iface:${i.device}`, label: `перевести группы на интерфейс ${i.device}${i.probe ? '' : ' (без проверки)'}` });
  }
  return opts;
});
const probeLocked = computed(() => isFree.value || isEng.value);

// другие пулы - кандидаты запасного пути умного каскада
const scPoolOptions = computed(() =>
  (status.data?.pools || [])
    .filter((p) => p.name !== props.poolName && !p.disabled)
    .map((p) => ({ value: p.name, label: p.name })));
const scModeOptions: UiSelectOption[] = [
  { value: 'schedule', label: 'по расписанию (в определённое время)' },
  { value: 'interval', label: 'по интервалу (раз в N часов)' },
  { value: 'manual', label: 'только кнопкой' },
];
const scTrigOptions: UiSelectOption[] = [
  { value: 'exhaust', label: 'после перебора всех конфигов пула' },
  { value: 'fails', label: 'после отказов подряд (быстрее)' },
];

watch(open, (v) => {
  if (!v) return;
  const p = (status.data?.pools || []).find((x) => x.name === props.poolName);
  if (!p) return;
  pool.value = p;
  members.value = [];
  freeConf.value = '';
  source.value = p.settings.source || '';
  newName.value = p.name;
  const ptype = /^https?:\/\//.test(p.settings.probeHost || '') ? 'http' : 'icmp';
  probeType.value = isEng.value ? 'http' : ptype;
  probeHost.value = p.settings.probeHost || (isFree.value ? 'http://1.1.1.1/' : 'http://www.gstatic.com/generate_204');
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
  exclNew.value = '';
  const sc = p.settings.smartCascade;
  const scRet = sc?.return || {};
  scOn.value = !!(sc?.via && sc.via.length);
  scVia.value = [...(sc?.via || [])]; // порядок = приоритет, храним как есть
  scMode.value = scRet.mode === 'interval' || scRet.mode === 'manual' ? scRet.mode : 'schedule';
  scTrigMode.value = sc?.triggerMode === 'fails' ? 'fails' : 'exhaust';
  scTrigFails.value = String((sc?.triggerFails || 0) > 0 ? sc!.triggerFails! : 3);
  scInterval.value = String((scRet.intervalH || 0) > 0 ? (scRet.intervalH as number) : 6);
  scTimes.value = (scRet.timeOfDay && scRet.timeOfDay.length ? scRet.timeOfDay : ['03:00']).join(', ');
  scDwell.value = String((sc?.minDwellMin || 0) > 0 ? sc!.minDwellMin! : 10);
  amnCountry.value = p.settings.amnezia?.country || '';
  premCountry.value = p.premium?.country || '';
  void loadMembers();
  void loadFreeConf(p);
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
      let msg = 'источник обновлён';
      if (plan.amnezia) msg = 'ключ Amnezia обменян' + (plan.amnezia.serverCountry ? ` (локация ${plan.amnezia.serverCountry})` : '');
      if (plan.added) msg += `: конфигов ${plan.added}`;
      if (plan.engine && plan.engine.length) msg += `, узлов ${plan.engine.length}`;
      if (plan.warnings && plan.warnings.length) msg += '; ' + plan.warnings.join('; ');
      push(msg, !!(plan.warnings && plan.warnings.length));
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

async function save(): Promise<void> {
  const p = pool.value;
  if (!p) return;
  await withBusy(async () => {
    try {
      const renamed = newName.value.trim();
      // умный каскад: полный объект при включении, пустой via = выключить
      let smartBody: Record<string, unknown> | undefined;
      if (!isEng.value) {
        const known = new Set(scPoolOptions.value.map((o) => o.value));
        const via = scVia.value.filter((v) => known.has(v)); // порядок списка = приоритет
        smartBody = scOn.value && via.length
          ? {
            smartCascade: {
              via,
              triggerMode: scTrigMode.value,
              ...(scTrigMode.value === 'fails' ? { triggerFails: Math.max(1, +scTrigFails.value || 3) } : {}),
              return: {
                mode: scMode.value,
                ...(scMode.value === 'interval' ? { intervalH: Math.max(1, +scInterval.value || 6) } : {}),
                ...(scMode.value === 'schedule' ? { timeOfDay: scTimes.value.split(/[,\s]+/).map((t) => t.trim()).filter(Boolean) } : {}),
              },
              minDwellMin: Math.max(0, +scDwell.value || 0),
            },
          }
          : { smartCascade: { via: [] } };
      }
      await a.update(p.name, {
        probeHost: probeHost.value.trim(),
        checkIntervalSec: +interval.value,
        failThreshold: +threshold.value,
        cooldownMin: +cooldown.value,
        keepalive: +keepalive.value,
        fallback: fallback.value,
        maxRttMs: rttOn.value ? +rtt.value || 0 : 0,
        updateIntervalH: updateInt.value === '' ? 0 : +updateInt.value || 0,
        excludes: [...exclList.value],
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

      <template v-if="members.length">
        <div class="sect">
          Состав пула (протоколы и адреса)
        </div>
        <div class="members">
          <div
            v-for="(m, i) in members"
            :key="i"
            class="member"
          >
            <UiBadge :text="m.proto + (m.detail ? '/' + m.detail : '')" />
            <!-- имя тоже может нести адрес (host|узел, файл-имя с хостом):
                 скрываем вместе с endpoint (слово владельца 2026-10-17) -->
            <span
              class="mname"
              :title="m.name"
              :class="{ secret: nameSecret(m) }"
            >{{ m.name }}</span>
            <span
              class="mend"
              :class="{ secret: !!m.endpoint }"
            >{{ m.endpoint || '' }}</span>
            <UiBadge
              v-if="m.active"
              text="активен"
              variant="ok"
            />
            <UiBadge
              v-if="m.enabled === false"
              text="выключен"
              variant="muted"
            />
          </div>
        </div>
      </template>

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
      <div class="sect">
        Исключения пула<UiHelp text="Эти адреса пул не обрабатывает - трафик на них и с них идёт напрямую, мимо туннеля (свои серверы за этим пулом и т.п.). Общий список на все пулы - на странице правил." />
      </div>
      <div
        v-if="exclList.length"
        class="excllist"
      >
        <span
          v-for="(e, i) in exclList"
          :key="e"
          class="exclitem"
        >
          {{ e }}
          <button
            type="button"
            class="exclrm"
            title="убрать из списка"
            @click="exclRemove(i)"
          >✕</button>
        </span>
      </div>
      <div class="hrow">
        <UiInput
          v-model="exclNew"
          bare
          placeholder="IP или подсеть; можно несколько через запятую/пробел"
          @keydown.enter="exclAdd"
        />
        <UiButton @click="exclAdd">
          + добавить
        </UiButton>
      </div>
      <template v-if="!isEng">
        <div class="sect">
          Умный каскад
        </div>
        <div class="hrow">
          <UiSwitch
            v-model="scOn"
            label="спасать пул через запасной при блокировке"
            tooltip="адрес VPN сохраняется: трафик пула идёт внутри туннеля запасного"
          />
          <UiHelp :text="G.smartCascade" />
        </div>
        <template v-if="scOn">
          <div class="hrow">
            <UiSelect
              v-model="scTrigMode"
              :options="scTrigOptions"
              label="Когда включать каскад"
            />
          </div>
          <UiInput
            v-if="scTrigMode === 'fails'"
            v-model="scTrigFails"
            type="number"
            label="Отказов подряд до каскада"
            tooltip="после N отказов активного конфига mawg сразу пробует ЭТОТ ЖЕ конфиг через запасной пул; не ожил - следующий конфиг напрямую, и так по кругу"
          />
          <UiOrderList
            v-model="scVia"
            :options="scPoolOptions"
            label="добавить запасной пул"
            empty-hint="Запасных нет: при блокировке пул не сможет уйти в каскад - добавьте хотя бы один пул."
            order-hint="Первый живой сверху становится запасным путём; порядок списка = приоритет."
          />
          <div class="hrow">
            <UiSelect
              v-model="scMode"
              :options="scModeOptions"
              label="Когда пробовать прямой путь обратно"
            />
          </div>
          <UiInput
            v-if="scMode === 'interval'"
            v-model="scInterval"
            type="number"
            label="Интервал проверки, часов"
          />
          <UiInput
            v-if="scMode === 'schedule'"
            v-model="scTimes"
            label="Время проверки, ЧЧ:ММ через запятую"
            tooltip="например: 03:00, 05:30 - в эти моменты панель пробует прямой путь и снимает каскад, если он ожил"
          />
          <UiInput
            v-model="scDwell"
            type="number"
            label="Антифлап: минут в состоянии до авто-возврата"
            tooltip="0 = 10 минут; кнопка «проверить сейчас» гейт минует"
          />
          <UiHint text="При срабатывании создаётся выключенная служебная группа «Служебная: этот пул через запасной». Пробы возврата идут в коротком окне без её правила: на секунды трафик пробует прямой путь." />
        </template>
      </template>
    </template>
    <template #footer>
      <UiButton @click="open = false">
        Отмена
      </UiButton>
      <UiButton
        variant="primary"
        :busy="busy"
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
.members {
  display: grid;
  gap: 4px;
  max-height: 180px;
  overflow: auto;
  font-size: 12px;
}
.member {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}
.mname {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mend {
  color: var(--dim);
  font-size: 11px;
}
.excllist {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.exclitem {
  display: inline-flex;
  gap: 6px;
  align-items: center;
  font-size: 12px;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 3px 8px;
}
.exclrm {
  background: none;
  border: 0;
  color: var(--dim);
  cursor: pointer;
  padding: 0 2px;
  font-size: 12px;
}
.exclrm:hover {
  color: var(--err);
}
</style>
