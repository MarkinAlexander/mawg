<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  UiBadge, UiButton, UiCard, UiDialog, UiHelp, UiHint, UiIcon, UiInput,
  UiSelect, type UiSelectOption, UiSwitch,
} from '../../components/ui'
import { useBundles } from '../../stores/bundles'
import { useConfirm } from '../../stores/confirm'
import { useIfaces } from '../../stores/ifaces'
import { usePools } from '../../stores/status'
import { useToast } from '../../stores/toast'
import type { BundleView, IfaceInfo, ProbeConfig } from '../../api/types'
import { PROBE_DEFAULTS, probeBadgeOf, probeTypeOf, targetOnTypeChange } from './ifacesLogic'
import { poolProbeBadge } from './ifacesLogic'
import { agoText } from './poolsLogic'
import { G } from '../glossary'
import BundleDialog from './BundleDialog.vue'
import CaptchaHost from './CaptchaHost.vue'
import PoolCard from './PoolCard.vue'
import PoolCreateDialog from './PoolCreateDialog.vue'
import PoolSettingsDialog from './PoolSettingsDialog.vue'
import WarpGenerateDialog from './WarpGenerateDialog.vue'
import SmartCascadeChip from './SmartCascadeChip.vue'

// Фаза 3.4 «Интерфейсы + WAN-probe» + Фаза 3.5 «Пулы» (44-дока §5.4-5.5).
// WAN-блок в легаси живёт в карточке Системы, пулы - в шапке; по слову
// владельца (2026-10-14) всё собрано на этой вкладке, Обзор - под выжимку.
// Опрос 30с таб-локальный (легаси startPanel), WAN поллинга не имеет.

const ifaces = useIfaces();
const { pools } = usePools();
// умный каскад пула для строки интерфейса (чип без захода в настройки)
function poolSmart(name?: string) {
  const p = (pools.value || []).find((x) => x.name === name);
  return p && p.settings?.smartCascade?.via?.length
    ? { settings: p.settings.smartCascade!, state: p.smart || null }
    : null;
}
const { push } = useToast();
const { confirm } = useConfirm();

const createOpen = ref(false);
const settingsFor = ref('');
const warpOpen = ref(false);
const bundles = useBundles();
const bundleDlgOpen = ref(false);
const bundleEditing = ref<BundleView | null>(null);

onMounted(() => {
  ifaces.start(30000);
  bundles.start(30000); // легаси: setInterval(refreshBundles, 30000)
  void ifaces.refreshWan();
});
onBeforeUnmount(() => {
  ifaces.stop();
  bundles.stop();
});

async function delBundle(b: BundleView): Promise<void> {
  const ok = await confirm({
    title: 'Удаление набора',
    text: `Удалить набор ${b.name}? Группы останутся на текущем интерфейсе.`,
    okLabel: 'Удалить',
  });
  if (!ok) return;
  try {
    await bundles.del(b.name);
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
}

// --- список интерфейсов (легаси renderIfaces 1060) ---
const showHidden = ref(false);
const hiddenCount = computed(() => ifaces.state.list.filter((i) => i.mode === 'hidden').length);
const rows = computed(() =>
  ifaces.state.list
    .filter((i) => i.mode !== 'hidden' || showHidden.value)
    .map((it) => ({
      it,
      title: rowTitle(it),
      hs: it.mode === 'managed'
        ? ''
        : it.handshakeAgo >= 0 ? `handshake ${it.handshakeAgo}с назад` : 'handshake нет',
      // бейдж внешней пробы - только не-managed строкам; managed несёт
      // бейдж пробы ПУЛА (ppb) из тех же данных
      pb: it.mode !== 'managed' && it.probeStatus ? probeBadgeOf(it.probeStatus) : null,
      // проба пула в managed-строке: тот же единственный вердикт и время
      ppb: it.mode === 'managed' ? poolProbeBadge(it) : null,
      pwhen: it.mode === 'managed' ? agoText(it.probeAt) : '',
    })));

// заголовок строки: слот+описание или описание (легаси title в renderIfaces)
function rowTitle(it: IfaceInfo): string {
  if (it.slot && it.slot !== it.device)
    return it.slot + (it.description && it.description !== it.slot ? ' - ' + it.description : '');
  return it.description && it.description !== it.device ? it.description : '';
}

const modeBusy = ref('');
async function toggleMode(it: IfaceInfo): Promise<void> {
  modeBusy.value = it.device;
  try {
    await ifaces.setMode(it.device, it.mode === 'external' ? 'hidden' : 'external');
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  modeBusy.value = '';
}

// бейдж пробы: клик - развёрнутая причина из скобок (легаси probeStatusBadge)
async function showProbeDetail(it: IfaceInfo): Promise<void> {
  const pb = it.probeStatus ? probeBadgeOf(it.probeStatus) : null;
  if (!pb?.detail) return;
    await confirm({ title: 'Проверка ' + it.device, text: pb.detail, okLabel: 'Закрыть', info: true });
}

// --- диалог пробы интерфейса (легаси openIfaceProbeDlg 1124) ---
const dlg = ref<{
  open: boolean
  device: string
  type: string
  target: string
  rttOn: boolean
  rtt: string
  saving: boolean
} | null>(null);

const probeTypeOptions: UiSelectOption[] = [
  { value: 'http', label: 'HTTP-страница 204' },
  { value: 'icmp', label: 'Ping IP' },
];
const wanTypeOptions: UiSelectOption[] = [
  { value: 'http', label: 'HTTP-страница (код 204)' },
  { value: 'icmp', label: 'Ping IP' },
];

function openProbeDlg(it: IfaceInfo): void {
  const p: Partial<ProbeConfig> = it.probe || {};
  const ptype = p.type || probeTypeOf(p.target || '') || 'http';
  dlg.value = {
    open: true,
    device: it.device,
    type: ptype,
    target: p.target || PROBE_DEFAULTS[ptype] || '',
    rttOn: (p.maxRttMs || 0) > 0,
    rtt: String((p.maxRttMs || 0) > 0 ? p.maxRttMs : 300),
    saving: false,
  };
}
const dlgOpen = computed({
  get: () => dlg.value?.open ?? false,
  set: (v: boolean) => {
    if (dlg.value) dlg.value.open = v;
  },
});
const dlgTitle = computed(() => (dlg.value ? `Проверка интерфейса ${dlg.value.device}` : ''));

// смена типа: пустое и дефолт своего типа -> дефолт нового (легаси
// wireProbeSwitch); обработчиком у селекта, не watch'ем - заполнение
// формы из ответа сервера не должно дёргать цель
function onProbeTypeChange(t: string): void {
  if (!dlg.value) return;
  dlg.value.type = t;
  dlg.value.target = targetOnTypeChange(dlg.value.target, t);
}

async function saveProbe(): Promise<void> {
  const d = dlg.value;
  if (!d) return;
  const target = d.target.trim();
  if (!target) {
    push('Укажите цель пробы', true);
    return;
  }
  d.saving = true;
  try {
    await ifaces.setProbe(d.device, { type: d.type, target, maxRttMs: d.rttOn ? +d.rtt || 0 : 0 });
    dlg.value = null;
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
    d.saving = false;
  }
}

async function removeProbe(): Promise<void> {
  const d = dlg.value;
  if (!d) return;
  d.saving = true;
  try {
    await ifaces.setProbe(d.device, { target: '' });
    dlg.value = null;
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
    d.saving = false;
  }
}

// --- проверка канала провайдера (легаси refreshWAN 1161/wanSave 1177) ---
const wanOn = ref(false);
const wanType = ref('http');
const wanTarget = ref('');
const wanRttOn = ref(false);
const wanRtt = ref('300');
const wanSaving = ref(false);

// поля формы перечитываются с сервера при загрузке и после сохранения
// (легаси refreshWAN): ptype из probe.type или по цели, при выключенной -
// пустая цель, http добирает дефолт
function fillWan(r: { enabled: boolean; probe: { type?: string; target?: string; maxRttMs?: number } | null }): void {
  const probe = r.probe || {};
  const ptype = r.enabled ? probe.type || probeTypeOf(probe.target || '') : 'http';
  wanType.value = ptype;
  wanTarget.value = r.enabled ? probe.target || '' : '';
  if (ptype === 'http' && !wanTarget.value) wanTarget.value = PROBE_DEFAULTS.http;
  wanRttOn.value = (probe.maxRttMs || 0) > 0;
  wanRtt.value = String((probe.maxRttMs || 0) > 0 ? probe.maxRttMs : 300);
  wanOn.value = r.enabled;
}

watch(() => ifaces.state.wan, fillWan, { immediate: true, deep: true });

function onWanTypeChange(t: string): void {
  wanType.value = t;
  wanTarget.value = targetOnTypeChange(wanTarget.value, t);
}

async function saveWan(): Promise<void> {
  const on = wanOn.value;
  const wtype = wanType.value;
  let target = wanTarget.value.trim();
  if (on && !target && wtype === 'http') target = PROBE_DEFAULTS.http;
  if (on && !target) {
    push('Укажите цель проверки канала', true);
    return;
  }
  wanSaving.value = true;
  try {
    await ifaces.saveWan(
      on ? { type: wtype, target, maxRttMs: wanRttOn.value ? +wanRtt.value || 0 : 0 } : { target: '' },
    );
    push('Проверка канала ' + (on ? 'сохранена' : 'выключена'));
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  wanSaving.value = false;
}
</script>

<template>
  <div class="toolbar">
    <UiButton
      variant="primary"
      @click="createOpen = true"
    >
      + интерфейс
    </UiButton>
  </div>
  <PoolCard
    v-for="p in pools"
    :key="p.name"
    :pool="p"
    @settings="settingsFor = p.name"
  />

  <!-- id - якорь поиска (search.ts): генератор WARP, 77-док -->
  <UiCard
    id="card-warp"
    title="Генератор WARP"
    subtitle="Новые конфиги Cloudflare WARP: регистрация прямо с роутера, ключи генерируются локально, каждый конфиг проверяется живой пробой."
  >
    <div class="toolbar">
      <UiButton
        variant="primary"
        @click="warpOpen = true"
      >
        Сгенерировать конфиги
      </UiButton>
    </div>
    <UiHint text="Можно сделать несколько конфигов на разных адресах:портах - если провайдер придавит один, выживут остальные. Подход и списки подсетей - открытый проект warp-generator." />
  </UiCard>
  <WarpGenerateDialog
    v-model:open="warpOpen"
    @created="() => push('Пул WARP создан')"
  />
  <PoolCreateDialog v-model:open="createOpen" />
  <PoolSettingsDialog
    :open="settingsFor !== ''"
    :pool-name="settingsFor"
    @update:open="(v: boolean) => { if (!v) settingsFor = '' }"
  />
  <CaptchaHost />

  <!-- id - якоря для поиска по настройкам (SearchDialog, search.ts) -->
  <UiCard
    id="card-ifaces"
    title="Интерфейсы роутера"
    subtitle="WireGuard-интерфейсы системы. Пулы управляются mawg, внешние только показываются (статус, привязка групп), скрытые не видны нигде."
  >
    <template #actions>
      <span class="cnt">{{ ifaces.state.list.length }} шт, скрыто {{ hiddenCount }}</span>
      <UiButton
        v-if="hiddenCount"
        tooltip="служебные интерфейсы не видны нигде - в группах и наборах их выбрать нельзя"
        @click="showHidden = !showHidden"
      >
        {{ showHidden ? 'скрыть служебные' : `показать скрытые (${hiddenCount})` }}
      </UiButton>
      <UiHelp text="Строки списка: up/down - поднято ли физическое соединение; handshake - «рукопожатие» туннеля с сервером («N с назад» = туннель жив, обмена не было N секунд); бейдж проверки - вердикт периодической пробы канала, клик - подробная причина. Пулы управляются панелью (mawg), внешние интерфейсы только показываются." />
    </template>

    <UiHint
      v-if="!ifaces.state.loaded && ifaces.state.error"
      :text="'интерфейсы недоступны: ' + ifaces.state.error"
    />
    <UiHint
      v-else-if="!ifaces.state.loaded"
      text="загружаю интерфейсы роутера..."
    />
    <UiHint
      v-else-if="!rows.length"
      text="WireGuard-интерфейсы не найдены"
    />
    <div
      v-else
      class="iflist"
    >
      <div
        v-for="r in rows"
        :key="r.it.device"
        class="ifrow"
      >
        <b class="dev">{{ r.it.device }}</b>
        <span
          v-if="r.title"
          class="ttl"
        >{{ r.title }}</span>
        <UiBadge
          :text="r.it.linkUp ? 'up' : 'down'"
          :variant="r.it.linkUp ? 'ok' : (r.it.mode === 'hidden' ? 'muted' : 'err')"
          :tooltip="r.it.linkUp ? 'соединение поднято' : 'соединение опущено'"
        />
        <span
          v-if="r.hs"
          class="hs"
          :title="G.handshake"
        >{{ r.hs }}</span>
        <UiBadge
          v-if="r.pb"
          class="pbadge"
          :class="{ clickable: !!r.pb.detail }"
          :text="r.pb.short"
          :variant="r.pb.ok ? 'ok' : 'err'"
          :tooltip="r.it.probeStatus"
          @click="showProbeDetail(r.it)"
        />
        <span class="sp" />
        <span class="right">
          <template v-if="r.it.mode === 'managed'">
            <UiBadge :text="'пул ' + (r.it.pool || '')" />
            <UiBadge
              v-if="r.ppb"
              :text="r.ppb.short"
              :variant="r.ppb.ok ? 'ok' : 'err'"
              :tooltip="(r.it.probeStatus || '') + (r.pwhen ? ' · ' + r.pwhen : '')"
            />
            <SmartCascadeChip
              v-if="poolSmart(r.it.pool)"
              :settings="poolSmart(r.it.pool)!.settings"
              :state="poolSmart(r.it.pool)!.state"
            />
          </template>
          <template v-else>
            <UiButton
              v-if="r.it.mode === 'external'"
              variant="icon"
              :tooltip="r.it.probe ? 'проверка настроена' : 'настроить проверку'"
              @click="openProbeDlg(r.it)"
            >
              <UiIcon name="gear" /><span
                v-if="r.it.probe"
                class="star"
              >*</span>
            </UiButton>
            <UiButton
              :busy="modeBusy === r.it.device"
              @click="toggleMode(r.it)"
            >
              {{ r.it.mode === 'external' ? 'скрыть' : 'сделать внешним' }}
            </UiButton>
          </template>
        </span>
      </div>
    </div>
  </UiCard>

  <!-- легаси #bundcard; IA §6: Интерфейсы - интерфейсы, наборы, WAN -->
  <UiCard
    id="card-bundles"
    title="Наборы интерфейсов"
    subtitle="Трафик групп идёт в первого здорового участника набора; при отказе - в следующего, при восстановлении - обратно."
  >
    <template #actions>
      <UiHelp :text="G.bundle + ' Участники: пулы mawg и внешние интерфейсы. Группы правил привязываются к набору целиком - переключение для них незаметно.'" />
      <UiButton
        variant="primary"
        @click="bundleEditing = null; bundleDlgOpen = true"
      >
        + набор
      </UiButton>
    </template>
    <UiHint
      v-if="bundles.state.loaded && !bundles.state.list.length"
      text="Наборов нет. Создайте набор, добавьте участников (пулы, внешние интерфейсы) и привяжите группы."
    />
    <div
      v-for="b in bundles.state.list"
      :key="b.name"
      class="bundrow"
    >
      <b class="bundname">{{ b.name }}</b>
      <span class="bundchips">
        <template
          v-for="(m, i) in b.members || []"
          :key="m"
        >
          <span
            v-if="i"
            class="bundsep"
          >&gt;</span>
          <UiBadge
            :text="(b.memberDevices || {})[m] || m"
            :variant="(b.memberDevices || {})[m] === b.currentMember ? 'ok' : 'muted'"
            :tooltip="m"
          />
        </template>
      </span>
      <span class="bundcnt">групп: {{ (b.groups || []).length }}</span>
      <span class="sp" />
      <span class="bundcur">{{ b.currentMember ? 'сейчас: ' + b.currentMember : 'не активирован' }}</span>
      <UiButton
        variant="icon"
        tooltip="изменить набор"
        @click="bundleEditing = b; bundleDlgOpen = true"
      >
        <UiIcon name="pencil" />
      </UiButton>
      <UiButton
        variant="icon"
        danger-icon
        tooltip="удалить набор"
        @click="delBundle(b)"
      >
        <UiIcon name="trash" />
      </UiButton>
    </div>
  </UiCard>

  <BundleDialog
    v-model:open="bundleDlgOpen"
    :editing="bundleEditing"
  />

  <!-- легаси: блок в карточке Системы; IA (44-дока §6) - вкладка Интерфейсы -->
  <UiCard
    id="card-probe"
    title="Проверка канала провайдера"
    subtitle="Перед тем как засчитать отказ интерфейса, проверяется сам интернет. Если интернет недоступен или отвечает дольше порога - отказ интерфейса не засчитывается и ротация не запускается."
  >
    <div class="hrow">
      <UiSwitch
        v-model="wanOn"
        label="включить проверку канала провайдера"
        tooltip="включить проверку канала провайдера"
      />
      <UiHelp :text="G.probe" />
    </div>
    <div class="hrow">
      <UiSelect
        :model-value="wanType"
        :options="wanTypeOptions"
        label="тип проверки"
        @update:model-value="onWanTypeChange"
      />
      <UiHelp :text="G.probeHttp204" />
    </div>
    <UiInput
      v-model="wanTarget"
      label="цель проверки"
      :placeholder="PROBE_DEFAULTS[wanType]"
    >
      <template #action>
        <UiButton
          variant="primary"
          :busy="wanSaving"
          @click="saveWan"
        >
          сохранить
        </UiButton>
      </template>
    </UiInput>
    <div class="hrow">
      <UiSwitch
        v-model="wanRttOn"
        label="порог RTT"
        tooltip="включить порог RTT"
      />
      <UiHelp :text="G.rtt" />
    </div>
    <UiInput
      v-model="wanRtt"
      type="number"
      label="порог RTT, мс"
      :disabled="!wanRttOn"
    />
  </UiCard>

  <UiDialog
    v-model:open="dlgOpen"
    :title="dlgTitle"
    :busy="dlg?.saving"
    width="440px"
  >
    <template v-if="dlg">
      <UiHint text="Проверка идёт через этот интерфейс. Её вердикт - «здоровье» интерфейса в наборах и статус в списке выше." />
      <div class="hrow">
        <UiSelect
          :model-value="dlg.type"
          :options="probeTypeOptions"
          label="Тип проверки"
          @update:model-value="onProbeTypeChange"
        />
        <UiHelp :text="G.probeHttp204" />
      </div>
      <UiInput
        v-model="dlg.target"
        label="Цель проверки"
        :placeholder="PROBE_DEFAULTS[dlg.type]"
      />
      <div class="hrow">
        <UiSwitch
          v-model="dlg.rttOn"
          label="считать нерабочим при RTT выше"
          tooltip="включить порог RTT"
        />
        <UiHelp :text="G.rtt" />
      </div>
      <UiInput
        v-model="dlg.rtt"
        type="number"
        label="RTT, мс"
        :disabled="!dlg.rttOn"
      />
    </template>
    <template #footer>
      <UiButton
        variant="danger"
        :disabled="dlg?.saving"
        @click="removeProbe"
      >
        убрать проверку
      </UiButton>
      <UiButton
        :disabled="dlg?.saving"
        @click="dlgOpen = false"
      >
        Отмена
      </UiButton>
      <UiButton
        variant="primary"
        :busy="dlg?.saving"
        @click="saveProbe"
      >
        Сохранить
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.bundrow {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  padding: 6px 0;
  border-bottom: 1px solid var(--border);
}
.bundrow:last-of-type {
  border-bottom: 0;
}
.bundname {
  min-width: 60px;
}
.bundchips {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
}
.bundsep {
  color: var(--dim);
}
.bundcnt {
  color: var(--dim);
  font-size: 12px;
}
.sp {
  flex: 1;
}
.bundcur {
  color: var(--dim);
  font-size: 11px;
}
.toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 10px;
}
.cnt {
  color: var(--dim);
  font-size: 12px;
}
.ifrow {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  padding: 5px 0;
  border-bottom: 1px solid var(--border);
}
.ifrow:last-child {
  border-bottom: 0;
}
.dev {
  min-width: 64px;
}
.ttl {
  color: var(--dim);
  font-size: 12px;
}
.hs {
  color: var(--dim);
  font-size: 11px;
}
.pbadge.clickable {
  cursor: pointer;
}
.sp {
  flex: 1;
}
.right {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
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
.star {
  font-size: 10px;
  color: var(--ok);
}
input:disabled {
  opacity: 0.45;
}
</style>
