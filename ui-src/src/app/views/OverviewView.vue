<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { UiBadge, UiButton, UiCard, UiHint } from '../../components/ui'
import { api } from '../../api/client'
import { useStatus, usePools, type MtState } from '../../stores/status'
import { useGroups } from '../../stores/groups'
import { useIfaces } from '../../stores/ifaces'
import { useBundles } from '../../stores/bundles'
import { useToast } from '../../stores/toast'
import type { DnsProbeRow } from '../../api/types'

// Обзор (44-дока §6) - короткая выжимка: состояние MT и плитки-счётчики
// со ссылками на вкладки. Пулы живут на «Интерфейсах» (49-док), LAN-клиенты
// - в Настройках, мастера настроек встанут сюда в Фазе 5 (46-дока §2).
// «Проверка DNS» - канарейка-зонд уровней (63-док): A-запрос
// deb.torproject.org через каждый уровень и сверка с каноническим набором;
// это же - будущий зонд «DNS отвечает?» для каскада L1-L5 (46-дока §3).

const { state: status } = useStatus();
const { pools } = usePools();
const groups = useGroups();
const ifaces = useIfaces();
const bundles = useBundles();
const { push } = useToast();

onMounted(() => {
  groups.start(30000);
  ifaces.start();
  bundles.start();
  void groups.loadBackups();
});
onBeforeUnmount(() => {
  groups.stop();
  ifaces.stop();
  bundles.stop();
});

const MT_LABEL: Record<MtState, string> = {
  checking: 'проверяю...',
  yes: 'доступен',
  no: 'недоступен',
};
const MT_BADGE: Record<MtState, 'ok' | 'warn' | 'err'> = {
  checking: 'warn',
  yes: 'ok',
  no: 'err',
};

const rulesCount = computed(() =>
  groups.state.groups.reduce((n, g) => n + (g.rules?.length || 0), 0));
const cascadesCount = computed(() =>
  groups.state.groups.filter((g) => g.cascade).length);

// 1 правило/2 правила/5 правил
function plural(n: number, one: string, few: string, many: string): string {
  const m10 = n % 10;
  const m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return `${n} ${one}`;
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return `${n} ${few}`;
  return `${n} ${many}`;
}

// --- Проверка DNS (канарейка-зонд, 63-док) ---
const probeRows = ref<DnsProbeRow[]>([]);
const probeBusy = ref(false);

async function runDnsProbe(): Promise<void> {
  if (probeBusy.value) return;
  probeBusy.value = true;
  try {
    const r = await api<{ results: DnsProbeRow[] }>('GET', '/dns/probe');
    probeRows.value = r.results || [];
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  probeBusy.value = false;
}

function verdictText(r: DnsProbeRow): string {
  if (r.kind === 'tcp') return r.verdict === 'clean' ? 'доступен' : 'недоступен';
  switch (r.verdict) {
    case 'clean': return `чисто (${r.canonical}/${r.total})`;
    case 'spoof': return 'ПОДМЕНА';
    case 'noanswer': return 'нет ответа';
    default: return 'недоступен';
  }
}

function verdictVariant(r: DnsProbeRow): 'ok' | 'err' | 'muted' {
  if (r.verdict === 'clean') return 'ok';
  if (r.verdict === 'error') return 'muted';
  return 'err';
}

// tooltip строки: ошибка сети, иначе путь уровня с ногой сервиса
// (перебор ног, Фаза E: «dns.google -> 8.8.4.4»)
function probeTooltip(r: DnsProbeRow): string {
  if (r.detail) return r.detail;
  const via = r.ip ? `${r.server} (нога ${r.ip})` : r.server;
  return `${via} - ${r.kind}`;
}

// рабочий уровень - первый чистый (порядок уровней = приоритет эскалации)
const probeSummary = computed(() => {
  const rows = probeRows.value;
  if (!rows.length) return '';
  const clean = rows.find((r) => r.verdict === 'clean');
  const poisoned = rows.filter((r) => r.verdict === 'spoof' || r.verdict === 'noanswer').length;
  if (clean) {
    return `рабочий уровень: ${clean.level}` + (poisoned ? ` (проблемных уровней: ${poisoned})` : '');
  }
  return 'рабочих уровней нет - нужна эскалация (туннель)';
});

const tiles = computed(() => {
  const rules = rulesCount.value;
  const casc = cascadesCount.value;
  const groupSub = `${plural(rules, 'правило', 'правила', 'правил')}${casc ? `, ${plural(casc, 'каскад', 'каскада', 'каскадов')}` : ''}`;
  return [
    {
      to: '/next/interfaces', label: 'Интерфейсы', count: ifaces.state.list.length,
      sub: 'подключения роутера и наборы',
    },
    {
      to: '/next/interfaces', label: 'Пулы', count: pools.value.length,
      sub: 'конфиги AWG, слоты, ротация',
    },
    {
      to: '/next/groups', label: 'Группы правил', count: groups.state.groups.length,
      sub: groupSub,
    },
    {
      to: '/next/backups', label: 'Резервные копии', count: groups.state.backups.list.length,
      sub: 'полный архив, правила, конфиг MT',
    },
  ];
});
</script>

<template>
  <UiCard
    id="card-overview"
    title="Обзор"
    subtitle="сводка: детали живут на вкладках; поиск по настройкам - лупа в шапке (Ctrl+K)"
  >
    <div class="mtrow">
      <UiBadge
        :text="'magitrickle: ' + MT_LABEL[status.mt]"
        :variant="MT_BADGE[status.mt]"
      />
      <span
        v-if="status.data"
        class="meta"
      >{{ status.data.platform }} · mawg {{ status.data.version }}</span>
      <span
        v-else
        class="meta"
      >{{ status.error || 'загрузка...' }}</span>
    </div>

    <div class="tiles">
      <RouterLink
        v-for="t in tiles"
        :key="t.label"
        :to="t.to"
        class="tile"
      >
        <span class="count">{{ t.count }}</span>
        <span class="tlabel">{{ t.label }}</span>
        <span class="tsub">{{ t.sub }}</span>
      </RouterLink>
    </div>

    <!-- Проверка DNS (63-док): канарейка-зонд уровней; порядок строк =
         приоритет эскалации, первый чистый = рабочий уровень. TCP-строки -
         «интернет жив?» (46-док §3): сеть мертва целиком -> эскалацию
         DNS не трогаем -->
    <UiCard
      id="card-dnsprobe"
      class="dnscard"
      title="Проверка DNS и сети"
      subtitle="канарейка deb.torproject.org: сверка ответа каждого уровня с каноническим набором; TCP-пробы - «интернет жив?» мимо DNS"
    >
      <div class="probebar">
        <UiButton
          :busy="probeBusy"
          @click="runDnsProbe"
        >
          проверить
        </UiButton>
        <span
          v-if="probeSummary"
          class="probesum"
        >{{ probeSummary }}</span>
      </div>
      <div
        v-if="probeRows.length"
        class="kv"
      >
        <template
          v-for="r in probeRows"
          :key="r.level"
        >
          <b>{{ r.level }}</b>
          <span>
            <UiBadge
              :text="verdictText(r)"
              :variant="verdictVariant(r)"
              :tooltip="probeTooltip(r)"
            />
            <span
              v-if="r.ip"
              class="muted proms"
            >{{ r.ip }}</span>
            <span class="muted proms">{{ r.ms }} мс</span>
          </span>
        </template>
      </div>
      <UiHint
        v-else-if="!probeBusy"
        text="Кнопка запускает A-запрос канарейки через каждый уровень (локальный резолвер, dns-proxy MT, апстрим провайдера, DoH/DoT) и сверяет ответ с каноническим набором. Чисто = минимум один адрес из набора; ноль при непустом ответе = подмена или блок. DoH/DoT перебирают ноги сервиса (например 8.8.8.8 и 8.8.4.4) - мёртвая нога не роняет уровень. TCP-строки - «интернет жив?» без DNS: оба недоступны = сеть вниз, доступен только внутренний = DNS-эскалация имеет смысл."
      />
    </UiCard>

    <UiHint text="Журнал событий - вкладка «Система». Доступ и LAN-клиенты - «Настройки». Будущие мастера настройки появятся здесь (Фаза 5)." />
  </UiCard>
</template>

<style scoped>
.mtrow {
  display: flex;
  gap: 10px;
  align-items: center;
  flex-wrap: wrap;
}
.meta {
  color: var(--dim);
  font-size: 12px;
}
.tiles {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 10px;
  /* слово владельца: отступ от строки статуса над плитками */
  margin-top: 10px;
}
.tile {
  display: flex;
  flex-direction: column;
  gap: 2px;
  background: var(--panel2);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 10px 12px;
  text-decoration: none;
  color: var(--text);
}
.tile:hover {
  border-color: var(--accent);
}
.count {
  font-size: 22px;
  font-weight: 600;
}
.tlabel {
  font-size: 13px;
}
.tsub {
  color: var(--dim);
  font-size: 11px;
}
.dnscard {
  margin-top: 12px;
}
.probebar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.probesum {
  font-size: 12px;
  color: var(--text);
}
.proms {
  font-size: 11px;
  margin-left: 6px;
}
</style>
