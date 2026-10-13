<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  UiBadge, UiButton, UiCard, UiDialog, UiHint, UiIcon, UiInput,
  UiSelect, type UiSelectOption, UiSwitch,
} from '../../components/ui'
import { useConfirm } from '../../stores/confirm'
import { useIfaces } from '../../stores/ifaces'
import { useToast } from '../../stores/toast'
import type { IfaceInfo, ProbeConfig } from '../../api/types'
import { PROBE_DEFAULTS, probeBadgeOf, probeTypeOf, targetOnTypeChange } from './ifacesLogic'

// Фаза 3.4 «Интерфейсы + WAN-probe» (44-дока §5.4). WAN-блок в легаси
// живёт в карточке Системы - по финальной IA (44-дока §6) перевезён сюда.
// Опрос 30с таб-локальный (легаси startPanel), WAN поллинга не имеет.

const ifaces = useIfaces();
const { push } = useToast();
const { confirm } = useConfirm();

onMounted(() => {
  ifaces.start(30000);
  void ifaces.refreshWan();
});
onBeforeUnmount(() => ifaces.stop());

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
      pb: it.probeStatus ? probeBadgeOf(it.probeStatus) : null,
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
  await confirm({ title: 'Проба ' + it.device, text: pb.detail, okLabel: 'Закрыть', info: true });
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
  { value: 'http', label: 'HTTP 204' },
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
  <UiCard
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
        />
        <span
          v-if="r.hs"
          class="hs"
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
          <UiBadge
            v-if="r.it.mode === 'managed'"
            :text="'пул ' + (r.it.pool || '')"
          />
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

  <!-- легаси: блок в карточке Системы; IA (44-дока §6) - вкладка Интерфейсы -->
  <UiCard
    title="Проверка канала провайдера"
    subtitle="Перед тем как засчитать отказ интерфейса, проверяется основной канал интернета той же пробой. Канал недоступен или деградировал - отказ интерфейса не засчитывается, ротация не запускается."
  >
    <UiSwitch
      v-model="wanOn"
      label="включить проверку канала провайдера"
      tooltip="включить проверку канала провайдера"
    />
    <UiSelect
      :model-value="wanType"
      :options="wanTypeOptions"
      label="тип проверки"
      @update:model-value="onWanTypeChange"
    />
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
    <UiSwitch
      v-model="wanRttOn"
      label="порог RTT"
      tooltip="включить порог RTT"
    />
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
      <UiHint text="Проба идет через этот интерфейс. Используется для здоровья в наборах и показывается в панели интерфейсов." />
      <UiSelect
        :model-value="dlg.type"
        :options="probeTypeOptions"
        label="Тип пробы"
        @update:model-value="onProbeTypeChange"
      />
      <UiInput
        v-model="dlg.target"
        label="Цель пробы"
        :placeholder="PROBE_DEFAULTS[dlg.type]"
      />
      <UiSwitch
        v-model="dlg.rttOn"
        label="считать нерабочим при RTT выше"
        tooltip="включить порог RTT"
      />
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
.star {
  font-size: 10px;
  color: var(--ok);
}
input:disabled {
  opacity: 0.45;
}
</style>
