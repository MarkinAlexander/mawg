<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { UiBadge, UiBanner, UiButton, UiCard, UiHelp, UiHint } from '../../components/ui';
import { api } from '../../api/client';
import type { DnsProbeRow, DnsState, KeeneticDNSState, KeeneticOwn } from '../../api/types';
import { ownAdviceOf, ownEncryptedOf, ownPlainOf, ownPlainRealOf, ownRowsOf } from './dnsKeenetic';
import { verdictRu, verdictVariant } from './dnsLogic';
import { useToast } from '../../stores/toast';
import { useConfirm } from '../../stores/confirm';

// Keenetic-разрезы DNS-страницы (71-док/98-док): совет по усилению, режим
// ndm и собственные серверы роутера - только просмотр и пробы.
const props = defineProps<{ state: DnsState | null }>();

const { push } = useToast();
const { confirm } = useConfirm();

const isKeen = computed(() => props.state?.platform === 'keenetic');

// собственные серверы Keenetic (71-док): только просмотр + проба;
// reading running-config - панель их не меняет
const own = ref<KeeneticOwn | null>(null);
const ownLoaded = ref(false);
const ownProbing = ref(false);
const ownVerdicts = ref<Record<string, DnsProbeRow>>({});

async function loadOwn(): Promise<void> {
  try {
    own.value = await api<KeeneticOwn>('GET', '/dns/keenetic/own');
  } catch {
    own.value = null;
  }
  ownLoaded.value = true;
}
watch(isKeen, (v) => {
  if (v && !ownLoaded.value) void loadOwn();
}, { immediate: true });

// DNS самого роутера (98-док): граница «панель в ndm не пишет» остаётся
// дефолтом. Режимы записи ip name-server (spare/mt) ВЫВЕДЕНЫ (решение
// владельца 01.11, инцидент с ndm): ndm не принимает loopback/порты,
// перехват DNS клиентов делает сам MagiTrickle; легаси-записи
// пользователей снимаются сервером автоматически при открытии карточки.
const keenDNS = ref<KeeneticDNSState | null>(null);
const kdbBusy = ref(false);

async function loadKeenDNS(): Promise<void> {
  try {
    keenDNS.value = await api<KeeneticDNSState>('GET', '/dns/keenetic/dns');
  } catch {
    keenDNS.value = null; // не-Кинетик/ошибка - карточки не будет
  }
}
watch(isKeen, (v) => {
  if (v && keenDNS.value === null) void loadKeenDNS();
}, { immediate: true });

async function toggleKeenIntercept(): Promise<void> {
  if (kdbBusy.value || !keenDNS.value) return;
  const enable = !keenDNS.value.intercept;
  const ok = await confirm({
    title: enable ? 'Включить перехват DNS Keenetic?' : 'Выключить перехват DNS Keenetic?',
    text: enable
      ? 'Keenetic начнёт отвечать на ВСЕ запросы порта 53 - и клиентов с чужим DNS, и собственные; резолвером станут его апстримы (собственные серверы + режим выше).'
      : 'Запросы клиентов с чужим DNS и собственные запросы роутера к внешним серверам пойдут провайдеру без перехвата.',
    okLabel: enable ? 'Включить' : 'Выключить',
  });
  if (!ok) return;
  kdbBusy.value = true;
  try {
    const res = await api<{ ok: boolean; state: KeeneticDNSState }>('PUT', '/dns/keenetic/dns', { intercept: enable, confirm: true });
    keenDNS.value = res.state;
    push(enable ? 'Перехват DNS Keenetic включён' : 'Перехват DNS Keenetic выключен');
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  } finally {
    kdbBusy.value = false;
  }
}

const ownRows = computed(() => ownRowsOf(own.value));

const ownAdvice = computed(() => {
  const ownV = own.value;
  if (!ownV) return null;
  return ownAdviceOf(ownV, ownVerdicts.value, ownRows.value);
});

const ownEncryptedRows = computed(() => ownEncryptedOf(ownRows.value));
const ownPlainRows = computed(() => ownPlainOf(ownRows.value));
const ownPlainReal = computed(() => ownPlainRealOf(ownRows.value));

async function probeOwn(): Promise<void> {
  if (ownProbing.value) return;
  ownProbing.value = true;
  const next: Record<string, DnsProbeRow> = {};
  for (const e of ownRows.value) {
    if (!e.kind || !e.server) continue;
    try {
      let q = `/dns/probe?kind=${e.kind}&server=${encodeURIComponent(e.server)}`;
      if (e.sni) q += `&sni=${encodeURIComponent(e.sni)}`;
      const r = await api<{ results: DnsProbeRow[] }>('GET', q);
      if (r.results?.[0]) next[e.key] = r.results[0];
    } catch {
      // сервер останется без вердикта - не диагностика, а сеть
    }
  }
  ownVerdicts.value = next;
  ownProbing.value = false;
}
</script>

<template>
  <UiHint
    v-if="state && isKeen"
    text="Собственные серверы Keenetic панель не меняет - они обслуживают сам роутер: обновления, opkg и hostname-эндпоинты пулов (например WARP) резолвит именно он. Без хотя бы одного сервера (простой UDP или DoH/DoT) роутер оглохнет, даже если домашние устройства работают. Локальные записи (ip host) резолвит именно этот путь: если устройства смотрят в другой DNS (перехват MagiTrickle) - они эти записи не увидят. Управляемая часть DNS - резервные серверы MagiTrickle ниже (домашние устройства)."
  />
  <UiBanner
    v-if="state && isKeen && ownAdvice && !ownAdvice.encrypted && !ownAdvice.udpCount"
    kind="warn"
    text="На самом Keenetic не осталось ни одного DNS-сервера: системный резолвер роутера не может отправить ни одного запроса - обновления, opkg и поднятие пулов с доменными эндпоинтами (например WARP) не работают. Добавьте в Keenetic хотя бы один сервер (простой UDP или DoH); домашние устройства при этом продолжают работать - их DNS ведёт MagiTrickle."
  />

  <UiCard
    v-if="ownAdvice && (ownAdvice.encrypted || ownAdvice.udpCount)"
    id="card-dns-advice"
    class="dnscard"
    title="DNS на самом Keenetic"
    subtitle="панель эти записи не меняет - показывает и проверяет"
  >
    <UiHint text="Зоны ответственности: записи Keenetic - ваши (список ниже), панель ими не управляет. Панель ведёт только резервные серверы MagiTrickle - они ниже и обновляются автоматически." />
    <ul class="advice">
      <li>
        Эти записи обслуживает сам роутер: обновления, opkg, поднятие туннелей с доменными адресами. Домашние устройства их не видят - их DNS ведёт MagiTrickle («Резервные серверы» ниже).
      </li>
      <li v-if="ownAdvice.encrypted">
        Шифрованный DNS (DoH/DoT) настроен - ответы по нему подменить нельзя, это основа. Keenetic умеет держать DoH, DoT и простые серверы одновременно.
      </li>
      <template v-if="ownAdvice.udpCount">
        <li>
          Простые UDP отвечают быстрее, но не защищены: держите их, только если уверены, что на вашей сети подмены нет.
        </li>
        <li>
          На сетях с подменой простым серверам нужна антиподмена DNS -
          например
          <a
            href="https://github.com/AloofLibra/zator"
            target="_blank"
            rel="noopener"
          >zator</a> (добавляет zapret2).
          Антиподмена работает не на всех сетях: результат зависит от настроек ТСПУ и требует ручной настройки.
        </li>
      </template>
    </ul>
    <UiBanner
      v-if="ownAdvice.spoofSeen"
      kind="err"
      text="Проверка увидела подмену ответов на простых UDP-серверах - при такой сети простые серверы отдают неверные адреса, полагаться на них нельзя."
    />
  </UiCard>

  <UiCard
    v-if="isKeen && keenDNS"
    id="card-dns-router"
    class="dnscard"
    title="DNS самого роутера"
    subtitle="режим управления ndm - только по явному согласию"
  >
    <UiHint text="Сам роутер резолвит через собственные серверы Keenetic: обновления, opkg, поднятие пулов с доменными адресами (например WARP). Локальные записи (ip host) тоже знает только он: если устройства их не видят - проверьте индикатор «Перехват DNS MagiTrickle» выше: при перехвате устройства смотрят в MagiTrickle, а не сюда. Режимы записи ip name-server в ndm выведены из панели; ваши записи не трогаются." />
    <b>Перехват DNS прошивки Keenetic<UiHelp text="Опция самой прошивки (не mawg): Keenetic отвечает на ВСЕ запросы порта 53 - даже если на устройстве прописан чужой DNS (8.8.8.8). Тогда резолвером для всего становится Keenetic: его серверы и локальные записи (ip host) видят все устройства. Выключено - запросы с чужим DNS уходят провайдеру напрямую. При включённом перехвате MagiTrickle этот переключатель на путь устройств не влияет: их порт 53 уже забирает MagiTrickle." /></b>
    <span>
      <UiBadge
        :text="keenDNS.intercept ? 'включён' : 'выключен'"
        :variant="keenDNS.intercept ? 'ok' : 'muted'"
      />
      <UiButton
        :busy="kdbBusy"
        @click="toggleKeenIntercept"
      >
        {{ keenDNS.intercept ? 'Выключить' : 'Включить' }}
      </UiButton>
    </span>
    <UiHint text="Режимы записи ip name-server в ndm выведены из панели: Keenetic не принимает loopback и порты, а перехват DNS клиентов делает сам MagiTrickle. Если раньше включали «Через MagiTrickle» - откройте эту карточку, легаси-запись снимется автоматически. Собственные записи Keenetic панель не трогает." />
  </UiCard>

  <template v-if="state && isKeen">
    <div class="subhead">
      <b>Собственные серверы Keenetic<UiHelp text="DNS-серверы, прописанные в настройках самого Keenetic. Панель показывает их только для диагностики и не меняет их. Кнопка «Проверить» прогоняет контрольный запрос через каждый сервер и помечает «работает/не работает»." /></b>
      <UiButton
        :busy="ownProbing"
        @click="probeOwn"
      >
        Проверить
      </UiButton>
    </div>
    <div class="kv">
      <template
        v-for="e in ownEncryptedRows"
        :key="e.key"
      >
        <b :class="{ muted: !e.kind }">{{ e.label }}<UiHelp
          v-if="!e.kind"
          text="Этот тип серверов в настройках Keenetic не найден."
        /></b>
        <span>
          <UiBadge
            v-if="ownVerdicts[e.key]"
            :text="verdictRu[ownVerdicts[e.key].verdict] || ownVerdicts[e.key].verdict"
            :variant="verdictVariant(ownVerdicts[e.key].verdict)"
            :tooltip="ownVerdicts[e.key].detail || ''"
          />
          <span
            v-if="e.sub"
            class="muted"
          >{{ e.sub }}</span>
        </span>
      </template>
      <template v-if="ownPlainReal.length === 0 && ownPlainRows.length">
        <b class="muted">Простые (UDP)</b>
        <span class="muted">записей не найдено</span>
      </template>
    </div>
    <details
      v-if="ownPlainReal.length"
      class="plainmore"
    >
      <summary>Простые серверы по подключениям ({{ ownPlainReal.length }})</summary>
      <div class="kv">
        <template
          v-for="e in ownPlainRows"
          :key="e.key"
        >
          <b>{{ e.label }}</b>
          <span>
            <UiBadge
              v-if="ownVerdicts[e.key]"
              :text="verdictRu[ownVerdicts[e.key].verdict] || ownVerdicts[e.key].verdict"
              :variant="verdictVariant(ownVerdicts[e.key].verdict)"
              :tooltip="ownVerdicts[e.key].detail || ''"
            />
            <span
              v-if="e.sub"
              class="muted"
            >{{ e.sub }}</span>
          </span>
        </template>
      </div>
      <UiHint text="Записи привязаны к подключениям Keenetic: если туннель лежит, проверка его сервера честно покажет «недоступен». Панель эти записи не меняет." />
    </details>
  </template>
</template>

<style scoped>
.subhead {
  display: flex;
  gap: 10px;
  align-items: baseline;
  flex-wrap: wrap;
  margin-top: 12px;
}
</style>
