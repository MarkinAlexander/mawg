<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { UiBadge, UiBanner, UiButton, UiCard, UiDialog, UiHelp, UiHint, UiIcon, UiInput, UiSelect, type UiSelectOption, UiSwitch, UiTextarea } from '../../components/ui';
import { api } from '../../api/client';
import type { DnsProbeRow, DnsState, DnsUpstream, DnsZone, DnsZonesRes } from '../../api/types';
import { a2key, poolOptionsOf, zoneLiveMapOf, zoneOf, zoneProbeOkOf, zoneRemoveAskOf, zoneSaveAskOf, zoneToggleAskOf, zoneValidateOf, type ZoneForm } from './dnsZones';
import { verdictRu, verdictVariant } from './dnsLogic';
import { loopGuardOf, loopRiskOf, upTooltipOf } from './dnsServers';
import { useStatus } from '../../stores/status';
import { useToast } from '../../stores/toast';
import { useConfirm } from '../../stores/confirm';
import { useSecrets } from '../secrets';

// зонный DNS (форк rev13): зона -> свои серверы, путь через пул. Каскад и
// автопроверка серверов зоны живут в MT; полный отвал - SERVFAIL
// (fail-closed), переключатель fallback - сознательное исключение
const props = defineProps<{
  state: DnsState | null;
  // режим «mawg не лезет» (115-док): правка зон гасится, пробы живут
  managedOff?: boolean;
}>();

const { push } = useToast();
const { confirm } = useConfirm();
const { state: statusState } = useStatus();
const secrets = useSecrets();

const zonesRes = ref<DnsZonesRes | null>(null);
const zoneVerdicts = ref<Record<string, DnsProbeRow[]>>({});
const zoneProbingId = ref('');
const zonesBusy = ref(false);

const zonesCapable = computed(() => zonesRes.value?.capable === true);
const zonesList = computed<DnsZone[]>(() => zonesRes.value?.zones ?? []);
const zoneLive = computed(() => zoneLiveMapOf(zonesRes.value));

async function loadZones(): Promise<void> {
  try {
    zonesRes.value = await api<DnsZonesRes>('GET', '/dns/zones');
  } catch {
    zonesRes.value = null;
  }
}
onMounted(loadZones);

// форма зоны в диалоге: idx -1 = новая, иначе правка записи списка
const zDlg = ref(false);
const zForm = ref<ZoneForm | null>(null);
const zErr = ref('');

// готовые интерфейсы - ТОТ ЖЕ список, что в выборе группы у правил
// (/mt/interfaces: устройства групп и пулов MT с подписями; слово
// владельца: не показывать всё netlink-подряд), минус blackhole -
// зонный путь блокировкой не бывает
const mtIfaces = ref<{ device: string; title?: string }[]>([]);
async function loadSysIfaces(): Promise<void> {
  try {
    const r = await api<{ interfaces: { device: string; title?: string }[] }>('GET', '/mt/interfaces');
    mtIfaces.value = (r.interfaces || []).filter((i) => i.device && i.device !== 'blackhole');
  } catch {
    mtIfaces.value = []; // MT не отвечает - останутся только пулы
  }
}

const zPoolOptions = computed<UiSelectOption[]>(() => poolOptionsOf(statusState.data?.pools || [], mtIfaces.value));

const loopGuard = computed(() => loopGuardOf(props.state));

function loopRisk(a: string): boolean {
  return loopRiskOf(a, loopGuard.value, props.state?.self || []);
}

function openZoneNew(): void {
  zForm.value = { idx: -1, zone: '', servers: '', pool: '', fallbackOpen: false, enable: true };
  zErr.value = '';
  zDlg.value = true;
  void loadSysIfaces();
}

function openZoneEdit(i: number): void {
  const z = zonesList.value[i];
  zForm.value = { idx: i, zone: z.zone, servers: (z.upstreams || []).join('\n'), pool: z.pool || '', fallbackOpen: !!z.fallbackOpen, enable: !!z.enable };
  zErr.value = '';
  zDlg.value = true;
  void loadSysIfaces();
}

function zValidate(): string[] | null {
  const f = zForm.value;
  if (!f) return null;
  const r = zoneValidateOf(f, zonesList.value);
  if (r.servers === null) {
    zErr.value = r.err;
    return null;
  }
  return r.servers;
}

async function putZones(list: DnsZone[], ask?: { title: string; text: string; ok: string }): Promise<boolean> {
  const risky = list.flatMap((z) => (z.upstreams || []).filter(loopRisk));
  if (risky.length) {
    const ok = await confirm({
      title: ask?.title || 'Зонные DNS',
      text: 'Осторожно: ' + risky.join(', ') + ' - это служба DNS самого роутера (dnsmasq), а она сейчас направлена в MT: такой адрес замкнёт петлю. ' + (ask?.text || 'Применить изменения?'),
      okLabel: 'Всё равно применить',
    });
    if (!ok) return false;
  } else if (ask) {
    const ok = await confirm({ title: ask.title, text: ask.text, okLabel: ask.ok });
    if (!ok) return false;
  }
  zonesBusy.value = true;
  try {
    const r = await api<DnsZonesRes>('PUT', '/dns/zones', { zones: list, confirm: risky.length > 0 });
    for (const wn of r.warnings || []) push(wn, true);
    push('зонные DNS применены');
    await loadZones();
    zonesBusy.value = false;
    return true;
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  zonesBusy.value = false;
  return false;
}

async function saveZone(): Promise<void> {
  const f = zForm.value;
  if (!f || zonesBusy.value) return;
  const servers = zValidate();
  if (!servers) return;
  const zone = zoneOf(f, zonesList.value, servers, Date.now());
  const list = zonesList.value.slice();
  if (f.idx >= 0) list[f.idx] = zone;
  else list.push(zone);
  zDlg.value = false;
  await putZones(list, zoneSaveAskOf(zone, servers));
}

async function removeZone(i: number): Promise<void> {
  const z = zonesList.value[i];
  const ok = await confirm(zoneRemoveAskOf(z));
  if (!ok) return;
  await putZones(zonesList.value.filter((_, k) => k !== i));
}

async function toggleZone(z: DnsZone): Promise<void> {
  if (zonesBusy.value) return;
  const list = zonesList.value.map((x) => (x.id === z.id ? { ...x, enable: !x.enable } : x));
  const ok = await confirm(zoneToggleAskOf(z));
  if (!ok) return;
  await putZones(list);
}

async function probeZone(z: DnsZone): Promise<void> {
  if (zoneProbingId.value) return;
  zoneProbingId.value = z.id;
  try {
    const r = await api<{ results: DnsProbeRow[] }>('POST', `/dns/zones/${encodeURIComponent(z.id)}/probe`, {});
    zoneVerdicts.value = { ...zoneVerdicts.value, [z.id]: r.results || [] };
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  zoneProbingId.value = '';
}

function zoneProbeOk(z: DnsZone): boolean {
  return zoneProbeOkOf(zoneVerdicts.value[z.id]);
}

function upTooltip(u: DnsUpstream, z: DnsZone): string {
  return upTooltipOf(u, zoneProbeOk(z));
}
</script>

<template>
  <UiCard
    id="card-dns-zones"
    title="Зонные DNS"
    subtitle="домены зоны - у своего DNS через свой туннель, всё остальное - общий список"
  >
    <UiHint
      v-if="!zonesRes"
      text="Статус зонного DNS не получен - раздел появится, когда ответит модуль MT."
    />
    <UiHint
      v-else-if="!zonesCapable"
      text="Для зонных DNS нужен MagiTrickle 0.8.2-rev13+ - обновите MT в разделе «Система», и карточка заработает."
    />
    <template v-else>
      <UiHint
        v-if="managedOff"
        text="Режим «mawg не лезет»: правка зон отключена - включите «DNS управляется mawg» вверху вкладки. Проверка зон ниже работает."
      />
      <UiHint text="Зона (например corp.example.com) покрывает себя и все поддомены: запросы этих имён уходят к назначенным серверам зоны - по порядку, как в общих правилах. Если серверы зоны доступны только через туннель, при его отвале зона честно замолчит - имена не утекут в общий DNS." />

      <div
        v-if="!zonesList.length"
        class="kv"
      >
        <b class="muted">зон нет</b>
        <span class="muted">весь DNS идёт по общим правилам выше</span>
      </div>
      <div
        v-for="(z, i) in zonesList"
        :key="z.id"
        class="zonebox"
      >
        <div class="zrow">
          <UiSwitch
            :model-value="z.enable"
            :disabled="zonesBusy"
            :tooltip="z.enable ? 'выключить зону' : 'включить зону'"
            @update:model-value="() => toggleZone(z)"
          />
          <code
            class="zname"
            :class="{ off: !z.enable }"
          >{{ z.zone }}</code>
          <UiBadge
            v-if="!z.enable"
            variant="muted"
            text="выключена"
          />
          <UiBadge
            v-if="z.pool"
            variant="info"
            :text="'через ' + z.pool.replace(/^iface:/, '')"
            :tooltip="z.pool.startsWith('iface:')
              ? 'серверы зоны идут через готовый интерфейс (настроен вне панели): путь держит служебная группа dnszone-*'
              : 'серверы зоны доступны только внутри туннеля этого пула: путь до них держит служебная группа dnszone-*'"
          />
          <UiBadge
            v-else
            variant="muted"
            text="напрямую"
          />
          <UiBadge
            v-if="z.fallbackOpen"
            variant="warn"
            text="fallback"
            tooltip="при полном отвале серверов зоны запросы уйдут в общий список DNS (имена зоны увидит общий резолвер)"
          />
          <span class="zbtns">
            <UiButton
              variant="icon"
              :busy="zoneProbingId === z.id"
              :disabled="!z.enable || !!zoneProbingId"
              tooltip="проверить зону"
              @click="probeZone(z)"
            >
              <UiIcon name="refresh" />
            </UiButton>
            <UiButton
              variant="icon"
              :disabled="zonesBusy || managedOff"
              tooltip="изменить зону"
              @click="openZoneEdit(i)"
            >
              <UiIcon name="pencil" />
            </UiButton>
            <UiButton
              variant="icon"
              danger-icon
              :disabled="zonesBusy || managedOff"
              tooltip="удалить зону"
              @click="removeZone(i)"
            >
              <UiIcon name="trash" />
            </UiButton>
          </span>
        </div>
        <div class="zservers">
          <!-- без живого статуса - просто список адресов; с ним адрес уже
               в каждом бейдже, второй раз не дублируем (жалоба: адрес
               светился трижды) -->
          <template v-if="!zoneLive[z.zone]">
            <span
              v-for="a in z.upstreams"
              :key="a"
              class="zsrv"
              :class="{ secret: secrets.line(a) }"
            >{{ a }}</span>
          </template>
          <template v-else>
            <UiBadge
              v-for="(u, ui) in zoneLive[z.zone].upstreams"
              :key="a2key(z, ui)"
              :text="(u.alive ? 'жив' : 'мёртв') + ': ' + u.address"
              :variant="u.alive ? 'ok' : 'err'"
              :tooltip="upTooltip(u, z)"
            />
          </template>
        </div>
        <!-- «Шрёдингер»: health-воркер MT считает мёртвым, а проба через
             путь клиентов проходит - объясняем, а не показываем оба
             вердикта как противоречие -->
        <UiHint
          v-if="zoneLive[z.zone] && zoneProbeOk(z) && zoneLive[z.zone].upstreams.some((u) => !u.alive)"
          text="Проба через путь клиентов проходит, но health-воркер MT ещё считает сервер мёртвым: после отвалов он возвращает сервер только после N успешных проверок подряд (гистерезис возврата, настраивается выше). Пользоваться зоной можно - вердикты догонят друг друга."
        />
        <div
          v-if="zoneVerdicts[z.id]"
          class="kv zprobe"
        >
          <template
            v-for="r in zoneVerdicts[z.id]"
            :key="r.level"
          >
            <b>{{ r.level }}</b>
            <span>
              <UiBadge
                :text="verdictRu[r.verdict] || r.verdict"
                :variant="verdictVariant(r.verdict)"
                :tooltip="r.detail || ''"
              />
              <span class="muted">{{ r.ms }} мс</span>
            </span>
          </template>
        </div>
      </div>

      <div class="dnsactions">
        <UiButton
          :busy="zonesBusy"
          :disabled="managedOff"
          @click="openZoneNew"
        >
          + зона
        </UiButton>
      </div>
      <div class="hrow">
        <UiHint text="Проверка зоны: контрольный запрос через каждый сервер зоны по пути клиентов - если сервер доступен только в туннеле, «недоступен» при лежачем туннеле будет честным. Два прогона: первый греет туннель." />
        <UiHelp text="Как зона находит путь: для каждой зоны с туннелем заводится служебная группа dnszone-* - адреса серверов зоны заворачиваются в выбранный пул. Лежачий пул = серверы зоны недостижимы = fail-closed." />
      </div>
    </template>
  </UiCard>

  <UiDialog
    v-model:open="zDlg"
    :busy="zonesBusy"
    title="Зонный DNS"
    width="520px"
  >
    <template v-if="zForm">
      <UiInput
        v-model="zForm.zone"
        label="Зона (себя и все поддомены)"
        mono
        placeholder="corp.example.com"
      />
      <UiHint text="Имя зоны без серверов: запросы любых имён внутри неё пойдут назначенным серверам зоны." />
      <UiTextarea
        v-model="zForm.servers"
        label="DNS-серверы зоны (по одному на строку, порядок = приоритет)"
        :rows="3"
        mono
        placeholder="10.8.0.1&#10;10.8.0.2:53 · tls://10.8.0.1 · https://dns.corp.example.com/dns-query"
      />
      <UiSelect
        v-model="zForm.pool"
        :options="zPoolOptions"
        label="Путь к серверам зоны"
      />
      <UiHint text="Пусто - серверы зоны должны быть доступны напрямую. Пул: адреса серверов заворачиваются в его туннель. Готовый интерфейс (например WG, настроенный вручную) - путь через него. При отвале пути зона замолчит, не утекая в общий DNS." />
      <UiSwitch
        v-model="zForm.fallbackOpen"
        label="При полном отвале - общий DNS (fallback)"
        tooltip="выключено (по умолчанию) - fail-closed: зона недоступна, имена не утекают; включено - при полном отвале серверов зоны запросы уйдут в общий список"
      />
      <UiSwitch
        v-model="zForm.enable"
        label="Зона включена"
      />
      <UiBanner
        v-if="zErr"
        kind="err"
        :text="zErr"
      />
    </template>
    <template #footer>
      <UiButton
        :busy="zonesBusy"
        @click="saveZone"
      >
        Сохранить и применить
      </UiButton>
      <UiButton
        :disabled="zonesBusy"
        @click="zDlg = false"
      >
        Отмена
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.dnsactions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 10px;
  align-items: center;
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
.zonebox {
  border: 1px solid var(--border, #3a3f4a);
  border-radius: 8px;
  padding: 8px 10px;
  margin-top: 10px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.zonebox .zrow {
  display: flex;
  gap: 6px;
  align-items: center;
  flex-wrap: wrap;
}
.zonebox .zname {
  font-weight: 600;
  overflow-wrap: anywhere;
}
.zonebox .zname.off {
  color: var(--dim);
}
.zonebox .zbtns {
  display: flex;
  gap: 4px;
  margin-left: auto;
  flex-wrap: wrap;
}
.zonebox .zservers {
  display: flex;
  gap: 6px;
  align-items: center;
  flex-wrap: wrap;
}
.zonebox .zsrv {
  font-family: var(--mono, monospace);
  font-size: 12px;
  overflow-wrap: anywhere;
}
.zonebox .zprobe {
  margin-top: 2px;
}
</style>
