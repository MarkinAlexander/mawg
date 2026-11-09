<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { UiBadge, UiBanner, UiButton, UiCard, UiHelp, UiHint } from '../../components/ui';
import { api } from '../../api/client';
import type { DnsManagedState, DnsState, DnsUpstreamsRes } from '../../api/types';
import { dnsmasqServersHelpOf, dnsmasqTextOf, upstreamsTextOf } from './dnsServers';
import DnsKeenetic from './DnsKeenetic.vue';
import DnsCascade from './DnsCascade.vue';
import DnsPolicyCard from './DnsPolicyCard.vue';
import DnsZonesCard from './DnsZonesCard.vue';
import { useToast } from '../../stores/toast';
import { useConfirm } from '../../stores/confirm';

// DNS-раздел: главный переключатель «DNS управляется mawg» (115-док,
// слово владельца 09.11: полное «не лезть» с живыми канарейками),
// состояние службы DNS, резервные серверы MT (каскад) и правила
// автопереключения L1-L5. Формулировки - «язык задач» (слово владельца
// 2026-10-16: без жаргона; подробности - по знакам вопроса UiHelp,
// самодокументация вместо ссылок на журналы сессий).
// Домены вынесены в соседние модули: dnsServers/dnsPolicy/dnsZones/
// dnsKeenetic (чистые функции) и DnsCascade/DnsPolicyCard/DnsZonesCard/
// DnsKeenetic (секции карточки).
const { push } = useToast();
const { confirm } = useConfirm();

const state = ref<DnsState | null>(null);
const cascade = ref<DnsUpstreamsRes | null>(null);
const managed = ref<DnsManagedState | null>(null);
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

async function loadManaged(): Promise<void> {
  try {
    managed.value = await api<DnsManagedState>('GET', '/dns/managed');
  } catch {
    managed.value = null; // старый бинарь без /dns/managed - дефолт-поведение
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
  await Promise.all([load(), loadCascade(), loadManaged()]);
}

let pollTimer: ReturnType<typeof setInterval> | null = null;
onMounted(() => {
  load();
  loadCascade();
  loadManaged();
  pollTimer = setInterval(loadCascade, 10000);
});
onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer);
});

const notSupported = computed(() => loadErr.value.includes('платформа'));
// 71-док: на Keenetic свои серверы роутера не трогаем - показываем
// только перехват MT_DNSOR и управляемую MT-часть
const isKeen = computed(() => state.value?.platform === 'keenetic');

// режим «mawg и MT не лезут» (115-док): редакторы гасим, канарейки живут
const managedOff = computed(() => managed.value?.managed === false);

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

// главный переключатель раздела (115-док задача B): off = правка настроек
// MagiTrickle (disableRemap53) + его перезапуск; тексты согласованы со
// словарём §0 дока - ip host видит только DNS Keenetic
async function toggleManaged(): Promise<void> {
  if (busy.value || !managed.value) return;
  const toOn = managedOff.value;
  const ok = await confirm({
    title: toOn ? 'Вернуть DNS-управление?' : 'Отключить DNS-управление?',
    text: toOn
      ? 'mawg вернёт перехват DNS MagiTrickle: правка настроек MagiTrickle и его перезапуск - краткий перерыв. Доменные группы MagiTrickle снова заработают. Проверки DNS (канарейки) продолжат работать.'
      : 'mawg и MagiTrickle перестанут вмешиваться в DNS: перехват порта 53 снимается (правка настроек MagiTrickle + его перезапуск), доменные группы MagiTrickle перестанут работать - пулы по устройствам продолжат. Устройства будут резолвить через DNS Keenetic - локальные записи (ip host) заработают. Проверки DNS (канарейки) останутся.',
    okLabel: toOn ? 'Включить' : 'Отключить',
  });
  if (!ok) return;
  busy.value = true;
  try {
    await api('PUT', '/dns/managed', { managed: toOn, confirm: true });
    push(toOn ? 'DNS-управление включено' : 'DNS-управление отключено: устройства резолвят через DNS Keenetic');
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
        <b>DNS управляется mawg<UiHelp text="Главный переключатель раздела. Включено - mawg и MagiTrickle ведут DNS домашних устройств (перехват порта 53 в dns-прокси MagiTrickle). Выключено («mawg не лезет») - перехват снимается (правка настроек MagiTrickle + его перезапуск), устройства резолвят через DNS Keenetic: работают его локальные записи (ip host) и Интернет-фильтр. Проверки DNS (канарейки) работают в обоих режимах - они идут с роутера напрямую." /></b>
        <span>
          <UiBadge
            v-if="managed"
            :text="managedOff ? 'выключено' : 'работает'"
            :variant="managedOff ? 'warn' : 'ok'"
          />
          <UiButton
            v-if="managed"
            :busy="busy"
            @click="toggleManaged"
          >
            {{ managedOff ? 'Включить' : 'Отключить' }}
          </UiButton>
        </span>
        <template v-if="isKeen">
          <b>Перехват DNS MagiTrickle<UiHelp text="DNS-запросы домашних устройств на адрес роутера (порт 53) перехватываются dns-прокси MagiTrickle (порт 3553) - дальше их ведут «Резервные серверы» ниже. Собственный запрос роутера к его IP тоже уходит в MagiTrickle, но локальные записи роутера (ip host) этим путём не резолвятся: их знает только DNS Keenetic. Техника с чужим DNS напрямую (8.8.8.8 на устройстве) не перехватывается вовсе - публичные резолверы уводит в туннель группа dns-tunnel, если она настроена." /></b>
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
          <b>Перенаправление<UiHelp text="DNS-запросы домашних устройств на адрес роутера (порт 53) принудительно идут через модуль MT. Техника с чужим DNS напрямую (8.8.8.8 на устройстве) не перехватывается - публичные резолверы уводит в туннель группа dns-tunnel, если она настроена." /></b>
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
        v-if="managedOff"
        kind="info"
        text="DNS никем не управляется: перехват MagiTrickle снят, устройства резолвят через DNS Keenetic - локальные записи (ip host) и Интернет-фильтр работают. Проверки ниже (канарейки) полностью доступны; редакторы серверов, зон и политики отключены."
      />

      <DnsKeenetic :state="state" />

      <DnsCascade
        :state="state"
        :cascade="cascade"
        :busy="busy"
        :managed-off="managedOff"
        :reload="loadCascade"
        :reload-all="reloadAll"
        @busy="busy = $event"
      />

      <div class="dnsactions">
        <UiButton
          v-if="!isKeen && !managedOff"
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
    :managed-off="managedOff"
    :reload-all="reloadAll"
  />

  <DnsZonesCard
    :state="state"
    :managed-off="managedOff"
  />
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
