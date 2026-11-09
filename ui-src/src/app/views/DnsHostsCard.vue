<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { UiBadge, UiButton, UiCard, UiDialog, UiHint, UiIcon, UiInput } from '../../components/ui';
import { api } from '../../api/client';
import type { DnsHostRecord, DnsHostsRes } from '../../api/types';
import { useToast } from '../../stores/toast';
import { useConfirm } from '../../stores/confirm';

// Карточка «Локальные записи» (115-док, слово владельца 09.11 третье +
// упрощение 10.11: «просто оставить инфу по доменам и адресам» - пробы
// из UI убраны, доступность сайта - забота пользователя; эндпоинт
// /dns/hosts/probe остался в API для диагностики): две вкладки -
// «Свои» (список mawg, применяется в резолвер роутера) и «Роутер»
// (его записи: ip host Keenetic, /etc/hosts + файл mawg OpenWrt).
const { push } = useToast();
const { confirm } = useConfirm();

const res = ref<DnsHostsRes | null>(null);
const tab = ref<'router' | 'own'>('own');
const busy = ref(false);

async function load(): Promise<void> {
  try {
    res.value = await api<DnsHostsRes>('GET', '/dns/hosts');
  } catch (e) {
    res.value = null;
    push(e instanceof Error ? e.message : String(e), true);
    return;
  }
  // свой список - редакторская копия стора; dirty до применения
  // (гард: старый бинарь/стаб может отдать объект без own)
  ownList.value = (res.value?.own ?? []).map((h) => ({ ...h }));
  ownDirty.value = false;
}
onMounted(load);

const isKeen = computed(() => res.value?.platform === 'keenetic');
const v6Capable = computed(() => res.value?.v6Capable === true);

// свой список: локальная редакторская копия + применяется целиком
const ownList = ref<DnsHostRecord[]>([]);
const ownDirty = ref(false);

// диалог своей записи
const dlgOwn = ref(false);
const dlgOwnId = ref('');
const dlgName = ref('');
const dlgIPv4 = ref('');
const dlgIPv6 = ref('');
const dlgErr = computed(() => {
  if (dlgOwnId.value === '' && ownList.value.some((h) => h.name === dlgName.value.trim().toLowerCase())) {
    return 'такое имя уже есть в списке';
  }
  return '';
});

function openOwnNew(): void {
  dlgOwnId.value = '';
  dlgName.value = '';
  dlgIPv4.value = '';
  dlgIPv6.value = '';
  dlgOwn.value = true;
}

function openOwnEdit(h: DnsHostRecord): void {
  dlgOwnId.value = h.id;
  dlgName.value = h.name;
  dlgIPv4.value = h.ipv4 ?? '';
  dlgIPv6.value = h.ipv6 ?? '';
  dlgOwn.value = true;
}

function saveOwnRow(): void {
  if (!dlgOwn.value || dlgErr.value) return;
  const rec: DnsHostRecord = {
    id: dlgOwnId.value,
    name: dlgName.value.trim().toLowerCase(),
    ipv4: dlgIPv4.value.trim(),
    ipv6: dlgIPv6.value.trim(),
  };
  const i = ownList.value.findIndex((h) => h.id === dlgOwnId.value);
  if (i >= 0) ownList.value.splice(i, 1, rec);
  else ownList.value.push(rec);
  ownList.value.sort((a, b) => a.name.localeCompare(b.name));
  ownDirty.value = true;
  dlgOwn.value = false;
}

async function removeOwnRow(h: DnsHostRecord): Promise<void> {
  const ok = await confirm({
    title: 'Убрать запись из списка?',
    text: `"${h.name}" уйдёт из своего списка и из резолвера роутера (по сохранении).`,
    okLabel: 'Убрать',
  });
  if (!ok) return;
  ownList.value = ownList.value.filter((x) => x.id !== h.id);
  ownDirty.value = true;
}

async function applyOwn(): Promise<void> {
  if (busy.value || !ownDirty.value) return;
  busy.value = true;
  try {
    const r = await api<{ ok: boolean; own: DnsHostRecord[] }>('PUT', '/dns/hosts', { hosts: ownList.value });
    ownList.value = r.own.map((h) => ({ ...h }));
    ownDirty.value = false;
    push('Свой список применён в резолвер роутера');
    await load();
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  busy.value = false;
}

// правка записи роутера (Keenetic: ip host; OpenWrt: только удаление
// строки /etc/hosts - свои записи идут в свой файл)
const dlgRouter = ref(false);
const dlgRouterName = ref('');
const dlgRouterIPv4 = ref('');

async function saveRouterRow(): Promise<void> {
  if (busy.value || !dlgRouter.value) return;
  busy.value = true;
  try {
    await api('POST', '/dns/hosts/router', { op: 'set', name: dlgRouterName.value, ipv4: dlgRouterIPv4.value });
    push(`Запись Keenetic обновлена: ${dlgRouterName.value}`);
    dlgRouter.value = false;
    await load();
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  busy.value = false;
}

async function removeRouter(e: { name: string; ipv4?: string; ipv6?: string }): Promise<void> {
  const ok = await confirm({
    title: 'Удалить запись роутера?',
    text: `"${e.name}" будет удалена из резолвера роутера` + (isKeen.value ? ' (no ip host в Keenetic).' : ' (строка /etc/hosts).'),
    okLabel: 'Удалить',
  });
  if (!ok) return;
  busy.value = true;
  try {
    await api('POST', '/dns/hosts/router', { op: 'del', name: e.name, ipv4: e.ipv4 ?? '', ipv6: e.ipv6 ?? '' });
    push(`Удалено: ${e.name}`);
    await load();
  } catch (er) {
    push(er instanceof Error ? er.message : String(er), true);
  }
  busy.value = false;
}
</script>

<template>
  <UiCard
    id="card-dns-hosts"
    class="dnscard"
    title="Локальные записи"
    subtitle="имена, которые отвечает сам роутер: свои + его записи"
  >
    <UiHint text="Две вкладки: «Свои» - ваш список (имя + IPv4 и/или IPv6), mawg применяет его в резолвер роутера; «Роутер» - то, что роутер уже знает (ip host в Keenetic, /etc/hosts в OpenWrt). Записи резолвера роутера видны устройствам, когда отвечать должен он сам - индикатор «Маршрутизация DNS» выше." />

    <UiBanner
      v-if="res && isKeen && !v6Capable"
      kind="info"
      text="Keenetic не умеет ipv6 host (в ndm такой команды нет): ipv6-половина своих записей на Keenetic не применяется - показывается честно. IPv6-записи доступны на OpenWrt."
    />

    <div class="tabs">
      <UiButton
        :variant="tab === 'own' ? 'primary' : 'ghost'"
        @click="tab = 'own'"
      >
        Свои ({{ ownList.length }})
      </UiButton>
      <UiButton
        :variant="tab === 'router' ? 'primary' : 'ghost'"
        @click="tab = 'router'"
      >
        Роутер ({{ res?.router?.length ?? 0 }})
      </UiButton>
    </div>

    <template v-if="tab === 'own'">
      <UiHint text="Имя + адрес: применяется в резолвер роутера (Keenetic: ip host - только IPv4; OpenWrt: свой файл dnsmasq /etc/hosts.d/mawg - IPv4 и IPv6)." />
      <div
        v-if="!ownList.length"
        class="kv"
      >
        <b class="muted">список пуст</b>
        <span class="muted">добавьте имя и адрес</span>
      </div>
      <div
        v-for="h in ownList"
        :key="h.id"
        class="hostrow"
      >
        <b class="hname">{{ h.name }}</b>
        <span>
          <code
            v-if="h.ipv4"
            class="addr"
          >{{ h.ipv4 }}</code>
          <code
            v-if="h.ipv6"
            class="addr"
          >{{ h.ipv6 }}</code>
          <UiButton
            variant="icon"
            tooltip="изменить"
            @click="openOwnEdit(h)"
          >
            <UiIcon name="pencil" />
          </UiButton>
          <UiButton
            variant="icon"
            danger-icon
            tooltip="убрать"
            @click="removeOwnRow(h)"
          >
            <UiIcon name="trash" />
          </UiButton>
        </span>
      </div>
      <div class="dnsactions">
        <UiButton
          :disabled="busy"
          @click="openOwnNew"
        >
          + запись
        </UiButton>
        <UiButton
          :busy="busy"
          :disabled="!ownDirty"
          @click="applyOwn"
        >
          Сохранить и применить
        </UiButton>
      </div>
    </template>

    <template v-if="tab === 'router'">
      <UiHint
        :text="isKeen
          ? 'Записи ip host из Keenetic (running-config): правка и удаление - командами ndm по явному действию, верифицируются чтением. Помечено «ваше» - имя есть в своём списке.'
          : 'Строки /etc/hosts и файл mawg (/etc/hosts.d/mawg): правка своих - через вкладку «Свои»; строку /etc/hosts можно удалить (точным совпадением), добавляйте записи через «Свои».'"
      />
      <div
        v-if="!res?.router?.length"
        class="kv"
      >
        <b class="muted">записей нет</b>
        <span class="muted">роутер ничего локального не отвечает</span>
      </div>
      <div
        v-for="e in res?.router ?? []"
        :key="e.name + (e.ipv4 ?? '') + (e.ipv6 ?? '')"
        class="hostrow"
      >
        <b class="hname">{{ e.name }}<UiBadge
          v-if="e.own"
          variant="info"
          text="ваше"
        /></b>
        <span>
          <code
            v-if="e.ipv4"
            class="addr"
          >{{ e.ipv4 }}</code>
          <code
            v-if="e.ipv6"
            class="addr"
          >{{ e.ipv6 }}</code>
          <UiBadge
            variant="muted"
            :text="e.source === 'ndm' ? 'ip host' : (e.source === 'mawg' ? 'файл mawg' : '/etc/hosts')"
          />
          <UiButton
            v-if="isKeen"
            variant="icon"
            tooltip="править ip host"
            @click="dlgRouterName = e.name; dlgRouterIPv4 = e.ipv4 ?? ''; dlgRouter = true"
          >
            <UiIcon name="pencil" />
          </UiButton>
          <UiButton
            variant="icon"
            danger-icon
            tooltip="удалить"
            @click="removeRouter(e)"
          >
            <UiIcon name="trash" />
          </UiButton>
        </span>
      </div>
    </template>
  </UiCard>

  <UiDialog
    v-model:open="dlgOwn"
    :title="dlgOwnId ? 'Править запись' : 'Новая запись'"
  >
    <UiInput
      v-model="dlgName"
      label="Имя (домен)"
      placeholder="nas.home или dzen.ru"
      mono
    />
    <UiInput
      v-model="dlgIPv4"
      label="IPv4"
      placeholder="192.168.1.10"
      mono
    />
    <UiInput
      v-if="v6Capable"
      v-model="dlgIPv6"
      label="IPv6"
      placeholder="fd00::10"
      mono
    />
    <UiHint
      v-if="!v6Capable"
      text="IPv6-поле недоступно: Keenetic не имеет команды ipv6 host - ipv6-часть на Keenetic не применяется."
    />
    <div
      v-if="dlgErr"
      class="dlgerr"
    >
      {{ dlgErr }}
    </div>
    <template #actions>
      <UiButton
        :disabled="!dlgName.trim() || (!dlgIPv4.trim() && !dlgIPv6.trim()) || !!dlgErr"
        @click="saveOwnRow"
      >
        Сохранить
      </UiButton>
      <UiButton @click="dlgOwn = false">
        Отмена
      </UiButton>
    </template>
  </UiDialog>

  <UiDialog
    v-model:open="dlgRouter"
    title="Править ip host в Keenetic"
  >
    <UiInput
      v-model="dlgRouterName"
      label="Имя (домен)"
      mono
    />
    <UiInput
      v-model="dlgRouterIPv4"
      label="IPv4"
      mono
    />
    <UiHint text="Запись будет переписана командой ndm (ip host) и проверена чтением running-config." />
    <template #actions>
      <UiButton
        :busy="busy"
        :disabled="!dlgRouterName.trim() || !dlgRouterIPv4.trim()"
        @click="saveRouterRow"
      >
        Применить
      </UiButton>
      <UiButton @click="dlgRouter = false">
        Отмена
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.tabs {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-top: 4px;
}
.hostrow {
  display: flex;
  gap: 10px;
  justify-content: space-between;
  align-items: center;
  padding: 4px 0;
  flex-wrap: wrap;
}
.hname {
  word-break: break-all;
}
.addr {
  margin-right: 6px;
}
.dlgerr {
  color: var(--err, #e05252);
  font-size: 12px;
  margin-top: 6px;
}
</style>
