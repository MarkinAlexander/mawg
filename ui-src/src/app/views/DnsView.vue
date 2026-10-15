<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { UiBadge, UiButton, UiCard, UiHint } from '../../components/ui';
import { api } from '../../api/client';
import type { DnsState } from '../../api/types';
import { useToast } from '../../stores/toast';
import { useConfirm } from '../../stores/confirm';

// DNS-раздел (46-док §3, срез OpenWrt 64-док): состояние DNS-стека
// (dnsmasq, remap53, апстримы MT) + переключение dnsmasq ↔ MT «живучим»
// профилем (список серверов прячется в uci, noresolv/cachesize не трогаем).
// Редактор апстримов MT и каскад уровней - следующие слои.
const { push } = useToast();
const { confirm } = useConfirm();

const state = ref<DnsState | null>(null);
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
onMounted(load);

const notSupported = computed(() => loadErr.value.includes('платформа'));

const dnsmasqText = computed(() => {
  const d = state.value?.dnsmasq;
  if (!d) return '';
  if (d.inMT) return d.servers.length > 1 ? `в MT + ещё (${d.servers.length})` : 'в MT (127.0.0.1#3553)';
  if (d.servers.length) return `свой список: ${d.servers.join(', ')}`;
  return 'напрямую (из resolvfile)';
});

const upstreamsText = computed(() => {
  const m = state.value?.mt;
  if (!m) return '';
  if (m.upstreams?.length) return `${m.upstreams.join(' → ')} (приоритет = порядок)`;
  return m.upstream || 'неизвестно';
});

async function toggle(): Promise<void> {
  if (!state.value || busy.value) return;
  const toMT = !state.value.dnsmasq.inMT;
  const ok = await confirm({
    title: toMT ? 'dnsmasq → MT' : 'dnsmasq → напрямую',
    text: toMT
      ? 'dnsmasq будет направлен в dns-proxy MT (127.0.0.1#3553). Прядевой список серверов спрячется в uci и вернётся при обратном переключении. Рестарт dnsmasq - краткий перерыв ответов.'
      : 'dnsmasq вернётся к прядевым серверам (рестор из uci), MT:3553 останется доступен напрямую. Рестарт dnsmasq.',
    okLabel: toMT ? 'Направить' : 'Вернуть',
  });
  if (!ok) return;
  busy.value = true;
  try {
    await api('POST', '/dns/dnsmasq', { mode: toMT ? 'mt' : 'direct' });
    push(toMT ? 'dnsmasq направлен в MT' : 'dnsmasq возвращён напрямую');
    await load();
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
    subtitle="состояние DNS-стека роутера и переключение dnsmasq (срез OpenWrt, 64-док)"
  >
    <template #actions>
      <UiBadge
        v-if="state"
        variant="ok"
        text="openwrt"
      />
    </template>

    <UiHint
      v-if="notSupported"
      text="DNS-менеджер пока реализован только для OpenWrt; Кинетик - после отладки OpenWrt (слово владельца)."
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
        <b>dnsmasq</b>
        <span>
          <UiBadge
            :text="dnsmasqText"
            :variant="state.dnsmasq.inMT ? 'ok' : (state.dnsmasq.servers.length ? 'warn' : 'muted')"
          />
          <UiBadge
            v-if="state.dnsmasq.strict"
            variant="err"
            text="строгий режим (SPOF)"
          />
        </span>
        <b>кэш dnsmasq</b>
        <span class="muted">{{ state.dnsmasq.cacheSize != null ? `${state.dnsmasq.cacheSize} записей` : 'не задан' }}</span>
        <b>ремап :53</b>
        <span>
          <UiBadge
            :text="state.mt.remap53 ? ':53 → MT (DNSOR)' : 'DNSOR не найден'"
            :variant="state.mt.remap53 ? 'ok' : 'muted'"
          />
        </span>
        <b>апстримы MT</b>
        <span class="muted">{{ upstreamsText }}</span>
      </div>

      <div class="dnsactions">
        <UiButton
          :busy="busy"
          :disabled="!state"
          @click="toggle"
        >
          {{ state?.dnsmasq.inMT ? 'Вернуть напрямую' : 'Направить dnsmasq в MT' }}
        </UiButton>
        <UiButton
          :busy="busy"
          @click="load"
        >
          Обновить
        </UiButton>
      </div>

      <UiHint text="«Живучий» профиль: dnsmasq держит MT:3553 параллельно интерфейсному upstream - смерть MT даёт деградацию до прямого DNS, не смерть (63-док). Апстримы MT (список с канарейкой-каскадом, rev6) редактируются пока в config.yaml MT; редактор в UI - следующий слой (46-док §3)." />
    </template>
  </UiCard>
</template>

<style scoped>
.dnsactions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 10px;
}
</style>
