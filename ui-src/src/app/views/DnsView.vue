<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { UiBadge, UiBanner, UiButton, UiCard, UiHelp, UiHint } from '../../components/ui';
import { api } from '../../api/client';
import type { DnsRoutingState, DnsState, DnsUpstreamsRes } from '../../api/types';
import { dnsmasqServersHelpOf, dnsmasqTextOf, upstreamsTextOf } from './dnsServers';
import DnsKeenetic from './DnsKeenetic.vue';
import DnsCascade from './DnsCascade.vue';
import DnsPolicyCard from './DnsPolicyCard.vue';
import DnsZonesCard from './DnsZonesCard.vue';
import DnsHostsCard from './DnsHostsCard.vue';
import { useToast } from '../../stores/toast';
import { useConfirm } from '../../stores/confirm';

// DNS-раздел: переключатель «Маршрутизация DNS» (115-док, слово владельца
// 09.11 второе: ловить запросы и вести трафик - суть MagiTrickle, не
// отключается; переключатель снимает только роль резолвера - «MT сам
// отправляет куда настроено»), состояние службы DNS, резервные серверы
// MT (каскад) и правила автопереключения L1-L5. Формулировки - «язык
// задач» (слово владельца 2026-10-16: без жаргона; подробности - по
// знакам вопроса UiHelp, самодокументация вместо ссылок на журналы
// сессий).
// Домены вынесены в соседние модули: dnsServers/dnsPolicy/dnsZones/
// dnsKeenetic (чистые функции) и DnsCascade/DnsPolicyCard/DnsZonesCard/
// DnsKeenetic (секции карточки).
const { push } = useToast();
const { confirm } = useConfirm();

const state = ref<DnsState | null>(null);
const cascade = ref<DnsUpstreamsRes | null>(null);
const routing = ref<DnsRoutingState | null>(null);
const loadErr = ref('');
const busy = ref(false);

async function load(): Promise<void> {
  loadErr.value = '';
  try {
    state.value = await api<DnsState>('GET', '/dns/state');
  } catch (e) {
    state.value = null;
    loadErr.value = e instanceof Error ? e.message : String(e);
  }
}

async function loadRouting(): Promise<void> {
  try {
    routing.value = await api<DnsRoutingState>('GET', '/dns/managed');
  } catch {
    routing.value = null; // старый бинарь без /dns/managed - дефолт-поведение
  }
}

async function loadCascade(): Promise<void> {
  try {
    cascade.value = await api<DnsUpstreamsRes>('GET', '/dns/upstreams');
  } catch {
    // MT недоступен/не отвечает - показываем «нет статуса», не шумим
    cascade.value = { capable: false };
  }
}

// перезагрузка после сохранения серверов/политики: и состояние, и каскад
async function reloadAll(): Promise<void> {
  await Promise.all([load(), loadCascade(), loadRouting()]);
}

let pollTimer: ReturnType<typeof setInterval> | null = null;
onMounted(() => {
  load();
  loadCascade();
  loadRouting();
  pollTimer = setInterval(loadCascade, 10000);
});
onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer);
});

const notSupported = computed(() => loadErr.value.includes('платформа'));
// 71-док: на Keenetic свои серверы роутера не трогаем - показываем
// только перехват MT_DNSOR и управляемую MT-часть
const isKeen = computed(() => state.value?.platform === 'keenetic');

// маршрутизация DNS выключена (115-док, слово владельца второе):
// редакторы гасим (списки не используются), канарейки и ловля живут
const routingOff = computed(() => routing.value?.routing === false);

const capable = computed(() => cascade.value?.capable === true && !!cascade.value.state);

const dnsmasqText = computed(() => dnsmasqTextOf(state.value?.dnsmasq));
const dnsmasqServersHelp = computed(() => dnsmasqServersHelpOf(state.value?.dnsmasq));
const upstreamsText = computed(() => upstreamsTextOf(state.value?.mt));

async function toggle(): Promise<void> {
  if (!state.value || busy.value) return;
  const toMT = !state.value.dnsmasq.inMT;
  const ok = await confirm({
    title: toMT ? 'DNS через модуль MT' : 'Возврат к прямым DNS',
    text: toMT
      ? 'DNS-запросы роутера будут обрабатываться модулем MagiTrickle (127.0.0.1#3553). Текущие серверы сохранятся в запасе и вернутся при обратном переключении. Рестарт службы DNS - краткий перерыв в ответах.'
      : 'Роутер вернётся к серверам провайдера напрямую; модуль MT останется доступен. Рестарт службы DNS - краткий перерыв в ответах.',
    okLabel: toMT ? 'Перевести' : 'Вернуть',
  });
  if (!ok) return;
  busy.value = true;
  try {
    await api('POST', '/dns/dnsmasq', { mode: toMT ? 'mt' : 'direct' });
    push(toMT ? 'DNS переведён через MT' : 'DNS возвращён к прямым серверам');
    await load();
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  busy.value = false;
}

// переключатель «Маршрутизация DNS» (115-док, слово владельца второе):
// off = апстримы форка подменяются на резолвер роутера (без рестарта MT);
// формулировки - по словарю §0 и платформе (ip host - Keenetic, hosts -
// OpenWrt); ловля запросов для маршрутизации трафика не трогается
const routerDNS = computed(() => (isKeen.value ? 'DNS Keenetic' : 'DNS роутера'));
const localRecords = computed(() => (isKeen.value ? 'локальные записи (ip host)' : 'локальные записи роутера (hosts)'));

async function toggleRouting(): Promise<void> {
  if (busy.value || !routing.value) return;
  const toOn = routingOff.value;
  const ok = await confirm({
    title: toOn ? 'Вернуть маршрутизацию DNS?' : 'Выключить маршрутизацию DNS?',
    text: toOn
      ? 'MagiTrickle снова станет отвечать на DNS-запросы устройств через свой каскад («Резервные серверы») и зоны - сохранённый список вернётся автоматически. Ловля запросов для маршрутизации трафика не прерывается.'
      : `MagiTrickle продолжит ловить DNS-запросы и вести трафик по группам - это не отключается. Снимается только его роль резолвера: отвечать будет ${routerDNS.value} (${localRecords.value} заработают), каскад «Резервных серверов» и зоны приостановятся и вернутся при включении. Пробы и статусы останутся.`,
    okLabel: toOn ? 'Включить' : 'Выключить',
  });
  if (!ok) return;
  busy.value = true;
  try {
    await api('PUT', '/dns/managed', { routing: toOn, confirm: true });
    push(toOn ? 'Маршрутизация DNS включена: отвечает каскад MagiTrickle' : `Маршрутизация DNS выключена: отвечает ${routerDNS.value}, трафик по группам ведётся как раньше`);
    await reloadAll();
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  busy.value = false;
}
</script>

<template>
  <UiCard
    id="card-dns"
    title="DNS"
    subtitle="состояние службы DNS и резервные серверы"
  >
    <template #actions>
      <UiBadge
        v-if="state?.platform"
        variant="ok"
        :text="state.platform"
      />
    </template>

    <UiHint
      v-if="notSupported"
      text="Управление DNS пока реализовано для OpenWrt; на Кинетике появится позже."
    />
    <template v-else>
      <UiHint
        v-if="loadErr && !state"
        :text="'ошибка: ' + loadErr"
      />
      <div
        v-if="state"
        class="kv"
      >
        <b>Маршрутизация DNS<UiHelp :text="'Вторая роль MagiTrickle: помимо ловли запросов для маршрутизации трафика (это работает всегда - индикатор «Перехват DNS MagiTrickle» ниже не трогается), он сам отвечает на запросы устройств через свой каскад («Резервные серверы»). Выключить - отвечать будет ' + routerDNS + ': ' + localRecords + ' заработают, ответы пойдут нативным путём роутера; каскад и зоны приостановятся и вернутся при включении. Наблюдение не отключается: пробы, статусы и журнал показывают, куда что пошло.'" /></b>
        <span>
          <UiBadge
            v-if="routing"
            :text="routingOff ? 'отвечает ' + routerDNS : 'через MT'"
            :variant="routingOff ? 'warn' : 'ok'"
          />
          <UiButton
            v-if="routing"
            :busy="busy"
            @click="toggleRouting"
          >
            {{ routingOff ? 'Включить' : 'Выключить' }}
          </UiButton>
        </span>
        <template v-if="isKeen">
          <b>Перехват DNS MagiTrickle<UiHelp text="Ловля DNS-запросов для маршрутизации трафика - суть MagiTrickle, работает всегда (в оба положения «Маршрутизации DNS»): запросы домашних устройств на адрес роутера (порт 53) перехватываются dns-прокси MagiTrickle (порт 3553) и наполняют доменные группы. Собственный запрос роутера к его IP тоже уходит в MagiTrickle. Техника с чужим DNS напрямую (8.8.8.8 на устройстве) не перехватывается вовсе - публичные резолверы уводит в туннель группа dns-tunnel, если она настроена." /></b>
          <span>
            <UiBadge
              :text="state.mt.remap53 ? 'работает' : 'выключен'"
              :variant="state.mt.remap53 ? 'ok' : 'muted'"
            />
          </span>
        </template>
        <template v-else>
          <b>Служба DNS<UiHelp text="dnsmasq - служба имён на самом роутере: через неё домашние устройства получают адреса сайтов. «Через MT» - запросы обрабатывает защитный модуль MagiTrickle (порт 3553); «свои серверы» - список задан вручную; «напрямую» - серверы провайдера." /></b>
          <span>
            <UiBadge
              :text="dnsmasqText"
              :variant="state.dnsmasq.inMT ? 'ok' : (state.dnsmasq.servers.length ? 'warn' : 'muted')"
            />
            <UiHelp
              v-if="dnsmasqServersHelp"
              :text="dnsmasqServersHelp"
            />
            <UiBadge
              v-if="state.dnsmasq.strict"
              variant="err"
              text="строгий режим (SPOF)"
              tooltip="профиль без запаса: единственный сервер умрёт - DNS встанет целиком"
            />
          </span>
          <b>Кэш адресов<UiHelp text="Память недавних ответов DNS: повторные сайты открываются быстрее, короткие сбои сервера не замечаются." /></b>
          <span class="muted">{{ state.dnsmasq.cacheSize != null ? `${state.dnsmasq.cacheSize} записей` : 'не задан' }}</span>
          <b>Перенаправление<UiHelp text="Ловля DNS-запросов для маршрутизации трафика: запросы домашних устройств на адрес роутера (порт 53) принудительно идут через модуль MT и наполняют доменные группы - работает всегда, независимо от «Маршрутизации DNS» выше. Техника с чужим DNS напрямую (8.8.8.8 на устройстве) не перехватывается - публичные резолверы уводит в туннель группа dns-tunnel, если она настроена." /></b>
          <span>
            <UiBadge
              :text="state.mt.remap53 ? 'весь :53 → MT' : 'не найдено'"
              :variant="state.mt.remap53 ? 'ok' : 'muted'"
            />
          </span>
        </template>
        <template v-if="!capable">
          <b>Серверы MT<UiHelp text="Список внешних DNS-серверов защитного модуля; порядок = приоритет. Здесь виден список из файла настроек: живой статус требует MT 0.8.2-rev8+." /></b>
          <span class="muted">{{ upstreamsText }}</span>
        </template>
      </div>

      <UiBanner
        v-if="routingOff"
        kind="info"
        :text="'На DNS-запросы устройств отвечает ' + routerDNS + ': ' + localRecords + (isKeen ? ' и Интернет-фильтр' : '') + ' видны. MagiTrickle продолжает ловить запросы и вести трафик по группам (перехват жив, доменные группы работают); каскад и зоны приостановлены. Проверки ниже (канарейки) полностью доступны; редакторы серверов, зон и политики отключены - их списки не используются.'"
      />

      <DnsKeenetic :state="state" />

      <DnsCascade
        :state="state"
        :cascade="cascade"
        :busy="busy"
        :routing-off="routingOff"
        :reload="loadCascade"
        :reload-all="reloadAll"
        @busy="busy = $event"
      />

      <div class="dnsactions">
        <UiButton
          v-if="!isKeen && !routingOff"
          :busy="busy"
          :disabled="!state"
          @click="toggle"
        >
          {{ state?.dnsmasq.inMT ? 'Вернуть на прямые DNS' : 'Направить DNS через MT' }}
        </UiButton>
        <UiButton
          :busy="busy"
          @click="load"
        >
          Обновить статус
        </UiButton>
      </div>

      <div
        v-if="!isKeen"
        class="kv"
      >
        <b>Надёжность<UiHelp text="«Режим повышенной надёжности»: DNS работает через MT, а серверы провайдера хранятся в запасе. Если MT остановится, служба DNS автоматически перейдёт на прямые серверы - интернет не пропадёт (возможно, вернётся реклама и фильтрация провайдера)." /></b>
        <span class="muted">серверы провайдера в запасе, смерть MT - переход, а не отказ</span>
      </div>
    </template>
  </UiCard>

  <DnsPolicyCard
    :state="state"
    :routing-off="routingOff"
    :reload-all="reloadAll"
  />

  <DnsZonesCard
    :state="state"
    :routing-off="routingOff"
  />

  <DnsHostsCard />
</template>

<style scoped>
.dnsactions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 10px;
  align-items: center;
}
</style>
