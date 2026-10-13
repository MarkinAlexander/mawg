<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiBadge, UiButton, UiDialog, UiHint, UiInput, UiSelect, type UiSelectOption, UiSwitch, UiTextarea } from '../../components/ui'
import { useIfaces } from '../../stores/ifaces'
import { usePoolActions } from '../../stores/pools'
import { useStatus } from '../../stores/status'
import { useCaptcha } from '../../stores/captcha'
import { useConfirm } from '../../stores/confirm'
import { useToast } from '../../stores/toast'
import type { PoolMember, PoolView } from '../../api/types'
import { fallbackHint } from './poolsLogic'

// Диалог настроек пула (легаси openSettings/spSave 935-1043): источник,
// переименование, Free (конфиг/перевыпуск/лок пробы), premium-страна,
// состав пула, проба, интервалы, фоллбек с подсказкой.
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
const members = ref<PoolMember[]>([]);
const freeConf = ref('');
const premCountry = ref('');
const amnCountry = ref('');
const busy = ref(false);

const isEng = computed(() => pool.value?.settings.engineMode === 'singbox');
const isFree = computed(() => !!pool.value?.free);
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
  const fb = p.settings.fallback || 'direct';
  fallback.value = fbOptions.value.some((o) => o.value === fb) ? fb : 'direct';
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
      await a.update(p.name, {
        probeHost: probeHost.value.trim(),
        checkIntervalSec: +interval.value,
        failThreshold: +threshold.value,
        cooldownMin: +cooldown.value,
        keepalive: +keepalive.value,
        fallback: fallback.value,
        maxRttMs: rttOn.value ? +rtt.value || 0 : 0,
        updateIntervalH: updateInt.value === '' ? 0 : +updateInt.value || 0,
      });
      const renamed = newName.value.trim();
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
        <UiTextarea
          v-model="source"
          label="Источник"
          :rows="3"
          mono
        />
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
        <UiHint text="Сервер сменил IP или ссылка протухла - вставьте новую и нажмите. Старые конфиги пула будут заменены разобранными. Для ключа Amnezia mawg повторит обмен (тем же устройством, без новой выдачи); можно сменить локацию списком." />
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
            <span
              class="mname"
              :title="m.name"
            >{{ m.name }}</span>
            <span class="mend">{{ m.endpoint || '' }}</span>
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
      <UiSelect
        v-model="probeType"
        :options="probeTypeOptions"
        label="Тип пробы через туннель"
        :disabled="probeLocked"
      />
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
      <UiSwitch
        v-model="rttOn"
        label="считать нерабочим при RTT выше"
        tooltip="включить порог RTT"
      />
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
      <UiInput
        v-model="threshold"
        type="number"
        label="Отказов подряд до ротации"
      />
      <UiInput
        v-model="cooldown"
        type="number"
        label="Cooldown неудачного конфига, мин"
      />
      <UiInput
        v-if="!isEng"
        v-model="keepalive"
        type="number"
        label="Keepalive, сек (0 = из конфига)"
      />
      <UiSelect
        v-model="fallback"
        :options="fbOptions"
        label="Если все конфиги недоступны"
      />
      <UiHint
        v-if="fbHintText"
        :text="fbHintText"
      />
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
</style>
