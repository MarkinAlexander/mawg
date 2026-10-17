<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { UiBadge, UiButton, UiCard, UiHelp, UiHint } from '../../components/ui'
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
    case 'clean': return 'работает';
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
// (перебор ног, Фаза E: «dns.google -> 8.8.4.4»); для DNS-уровней -
// сверка с эталоном («работает» = в ответе есть эталонный адрес)
function probeTooltip(r: DnsProbeRow): string {
  const bits: string[] = [];
  if (r.detail) bits.push(r.detail);
  else bits.push(r.ip ? `${r.server} (нога ${r.ip})` : r.server);
  if (r.kind !== 'tcp') bits.push(`эталонных адресов в ответе: ${r.canonical} из ${r.total}`);
  return bits.join(' · ');
}

// расшифровка [?] у каждой строки уровня (глоссарий-проход Обзора, слово
// владельца: DoH/DoT/TCP в подписях остаются - термины гуглятся, а
// расшифровка рядом). Ключи = Go-лейблы dnsprobe.Defaults; незнакомый
// лейбл (зонд по ?kind&server) - общая фраза по транспорту.
const LEVEL_HELP: Record<string, string> = {
  'Служба DNS роутера': 'Встроенная служба имён роутера (программа dnsmasq): через неё адреса сайтов получают все домашние устройства.',
  'DNS через MT': 'DNS-прокси защитного модуля (порт 3553). Запросы идут в цепочку резервных серверов - список и живой статус на вкладке «DNS».',
  'DNS провайдера': 'Сервер, выданный провайдером автоматически. На сетях с блокировками именно он часто отдаёт подменённые ответы.',
  'DoH Cloudflare': 'DoH (DNS over HTTPS): DNS-запросы спрятаны внутри обычного HTTPS-трафика (порт 443) - отсечь их выборочно нельзя. Сервер Cloudflare; проверяются оба адреса: 1.1.1.1 и 1.0.0.1.',
  'DoH Google': 'DoH (DNS over HTTPS), сервер Google. Проверяются оба адреса - 8.8.8.8 и 8.8.4.4: если один заблокирован, пробуется второй.',
  'DoT Google': 'DoT (DNS over TLS): шифрованный DNS на отдельном порту 853. Проверяется настоящим TLS-соединением с сервером Google (8.8.8.8/8.8.4.4).',
  'интернет TCP 1.1.1.1:443': 'Проверка связи без DNS: прямое соединение с зарубежным сервером. Оба таких уровня недоступны - интернета нет целиком, и выводы по DNS-уровням делать рано.',
  'интернет TCP 77.88.8.8:443': 'Проверка связи без DNS: прямое соединение с сервером Яндекса. Доступен он, а зарубежный - нет: сеть жива, но зарубежные каналы перекрыты, обход DNS через туннель имеет смысл.',
};
const KIND_HELP: Record<string, string> = {
  udp: 'обычный DNS-запрос', doh: 'DNS через HTTPS', dot: 'DNS через TLS', tcp: 'проверка связи без DNS',
};
function levelHelp(r: DnsProbeRow): string {
  if (LEVEL_HELP[r.level]) return LEVEL_HELP[r.level];
  return `Проверка уровня «${r.level}» (${KIND_HELP[r.kind] || r.kind}, сервер ${r.server}).`;
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
  return 'рабочих уровней нет - нужен обход через туннель (вкладка DNS)';
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
      <UiHelp text="MagiTrickle (MT) - защитный модуль на роутере: он делит трафик по группам правил и держит резервные DNS-серверы. Пул - набор однотипных подключений (интерфейс + конфиги AWG). Движок (sing-box) - сетевое ядро, через которое работают пулы. Каскад - группа, направляющая трафик в другую группу. Уровни L1-L5 - цепочка DNS-серверов с автопереключением (вкладка DNS)." />
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
         DNS не трогаем. Формулировки - «язык задач» (67-док): термины
         остаются в подписях, расшифровка - в [?] рядом -->
    <UiCard
      id="card-dnsprobe"
      class="dnscard"
      title="Проверка DNS и сети"
      subtitle="какие каналы DNS работают и не подменяют ответы; TCP-строки - жива ли сеть вообще"
    >
      <template #actions>
        <UiHelp text="По каждому уровню отправляется контрольный запрос к deb.torproject.org, ответ сверяется с эталонным набором адресов: честный сервер возвращает весь набор, подменённый - чужие адреса («ПОДМЕНА») или ничего («нет ответа»). Первый уровень с вердиктом «работает» - рабочий; порядок строк = приоритет." />
      </template>
      <div class="probebar">
        <UiButton
          :busy="probeBusy"
          @click="runDnsProbe"
        >
          Проверить
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
          <b>{{ r.level }}<UiHelp :text="levelHelp(r)" /></b>
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
    </UiCard>

    <UiHint text="Журнал событий - вкладка «Система». Доступ и LAN-клиенты - «Настройки». В планах - мастер по настройкам на этом месте; как и что он должен предлагать - ещё не придумано, идеи приветствуются." />
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
