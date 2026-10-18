<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { UiBadge, UiBanner, UiButton, UiCard, UiHelp, UiHint, UiInput, UiSelect, type UiSelectOption, UiSwitch, UiTextarea } from '../../components/ui';
import { api } from '../../api/client';
import type { DnsProbeRow, DnsState, DnsPolicyRes, DnsUpstream, DnsUpstreamsRes, KeeneticOwn } from '../../api/types';
import { useStatus } from '../../stores/status';
import { useToast } from '../../stores/toast';
import { useConfirm } from '../../stores/confirm';

// DNS-раздел: состояние службы DNS, резервные серверы MT (каскад) и
// правила автопереключения L1-L5. Формулировки - «язык задач» (слово
// владельца 2026-10-16: без внутреннего жаргона; подробности - по знакам
// вопроса UiHelp, самодокументация вместо ссылок на журналы сессий).
const { push } = useToast();
const { confirm } = useConfirm();
const { state: statusState } = useStatus();

const state = ref<DnsState | null>(null);
const cascade = ref<DnsUpstreamsRes | null>(null);
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

async function loadCascade(): Promise<void> {
  try {
    cascade.value = await api<DnsUpstreamsRes>('GET', '/dns/upstreams');
  } catch {
    // MT недоступен/не отвечает - показываем «нет статуса», не шумим
    cascade.value = { capable: false };
  }
}

let pollTimer: ReturnType<typeof setInterval> | null = null;
onMounted(() => {
  load();
  loadCascade();
  loadPolicy();
  pollTimer = setInterval(loadCascade, 10000);
});
onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer);
});

const notSupported = computed(() => loadErr.value.includes('платформа'));
// 71-док: на Keenetic свои серверы роутера не трогаем - показываем
// только перехват MT_DNSOR и управляемую MT-часть
const isKeen = computed(() => state.value?.platform === 'keenetic');

// --- собственные серверы Keenetic (только просмотр + проба, 71-док):
// reading running-config - панель их не меняет ---
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

const ownRows = computed(() => {
  if (!own.value) return [];
  const rows: { key: string; label: string; sub: string; kind?: string; server?: string; sni?: string }[] = [];
  if (!(own.value.doh || []).length) rows.push({ key: 'empty-doh', label: 'DoH', sub: 'записей не найдено' });
  for (const d of own.value.doh || []) {
    rows.push({ key: `doh|${d.uri}`, label: d.uri, sub: d.format ? `формат ${d.format}` : '', kind: 'doh', server: d.uri });
  }
  if (!(own.value.dot || []).length) rows.push({ key: 'empty-dot', label: 'DoT', sub: 'записей не найдено' });
  for (const t of own.value.dot || []) {
    const server = t.port ? `${t.address}:${t.port}` : `${t.address}:853`;
    rows.push({ key: `dot|${server}|${t.sni || ''}`, label: server, sub: t.sni ? `sni ${t.sni}` : '', kind: 'dot', server, sni: t.sni });
  }
  if (!(own.value.udp || []).length) rows.push({ key: 'empty-udp', label: 'Простые (UDP)', sub: 'записей не найдено' });
  for (const u of own.value.udp || []) {
    for (const srv of u.servers) rows.push({ key: `udp|${srv}`, label: srv, sub: `подключение ${u.interface}`, kind: 'udp', server: srv });
  }
  for (const u of own.value.v6 || []) {
    for (const srv of u.servers) rows.push({ key: `v6|${srv}`, label: srv, sub: `подключение ${u.interface} (IPv6)`, kind: 'udp', server: srv });
  }
  return rows;
});

// совет по усилению DNS на самом Keenetic (дизайн-слово владельца
// 2026-10-18: НЕ советовать простой UDP «запасным» - на сетях с подменой
// он первым отравляется; простые серверы - только при уверенности в
// отсутствии подмены или под антиподменой вроде zator)
const ownAdvice = computed(() => {
  const ownV = own.value;
  if (!ownV) return null;
  const encrypted = (ownV.doh || []).length + (ownV.dot || []).length;
  const udpCount = (ownV.udp || []).length + (ownV.v6 || []).length;
  const spoofSeen = Object.values(ownVerdicts.value).some(
    (r) => r.verdict === 'spoof' && ownRows.value.find((e) => e.key && e.kind === 'udp'),
  );
  return { encrypted, udpCount, spoofSeen };
});

// шифрованные (DoH/DoT) - всегда в кадре; простые по подключениям -
// свёрнуты: лежачие туннели дают честные «недоступен» на каждую запись
// и захламляют список (слово владельца: не вводить в заблуждение)
const ownEncryptedRows = computed(() =>
  ownRows.value.filter(
    (e) => e.kind === 'doh' || e.kind === 'dot' || e.key === 'empty-doh' || e.key === 'empty-dot',
  ),
);
const ownPlainRows = computed(() => ownRows.value.filter((e) => e.kind === 'udp' || e.key === 'empty-udp'));
const ownPlainReal = computed(() => ownPlainRows.value.filter((e) => e.kind === 'udp'));

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

const capable = computed(() => cascade.value?.capable === true && !!cascade.value.state);
const ups = computed(() => cascade.value?.state?.upstreams ?? []);
const health = computed(() => cascade.value?.state?.health);

const healthText = computed(() => {
  const h = health.value;
  if (!h) return '';
  if (!h.enabled) return 'автопроверка выключена';
  const gap = h.restoreGap.replace('m0s', ' мин').replace('m', ' мин').replace('s', ' с');
  return `отключение при ${h.failCycles} сбоях подряд, восстановление после ${h.restoreCycles} успешных проверок, пауза между переключениями ${gap}`;
});

function upTooltip(u: DnsUpstream): string {
  const bits: string[] = [];
  if (u.reason) bits.push('причина: ' + u.reason);
  if (u.lastSwap && !u.lastSwap.startsWith('0001-')) bits.push('переключён: ' + new Date(u.lastSwap).toLocaleString());
  bits.push(`сбои подряд: ${u.fails}, успешные подряд: ${u.oks}`);
  return bits.join(' · ');
}

const dnsmasqText = computed(() => {
  const d = state.value?.dnsmasq;
  if (!d) return '';
  if (d.inMT) return d.servers.length > 1 ? `через MT + ещё (${d.servers.length})` : 'через MT (127.0.0.1#3553)';
  // чип не растёт вместе с длиной списка (слово владельца: десятки серверов
  // вылезали за карточку) - полный список рядом, по клику [?]
  if (d.servers.length === 1) return `свой сервер: ${d.servers[0]}`;
  if (d.servers.length) return `свои серверы: ${d.servers.length}`;
  return 'напрямую (серверы провайдера)';
});

// полный список своих серверов для [?] у чипа (UiHelp: pre-line + свой
// скролл, длинный список не ломает вёрстку)
const dnsmasqServersHelp = computed(() => {
  const d = state.value?.dnsmasq;
  if (!d || !d.servers.length) return '';
  return 'Свои серверы dnsmasq (заданы вручную):\n' + d.servers.map((s) => '· ' + s).join('\n');
});

const upstreamsText = computed(() => {
  const m = state.value?.mt;
  if (!m) return '';
  if (m.upstreams?.length) return `${m.upstreams.join(' → ')} (первый - главный)`;
  return m.upstream || 'неизвестно';
});

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

// --- редактор списка серверов MT (порядок = приоритет) ---
const edit = ref<string[] | null>(null);
const newAddr = ref('');
const editList = computed(() => edit.value ?? []);

// петлезащита (openwrt, dnsmasq.inMT): 127.0.0.1:53/свой IP:53 - это
// dnsmasq, который сам кормится от MT - петля уронит DNS. На Кинетике
// 127.0.0.1:53 легален (системный резолвер) - правило платформенное.
const loopGuard = computed(() => !!state.value && state.value.platform !== 'keenetic' && state.value.dnsmasq.inMT);

function loopRisk(a: string): boolean {
  if (!loopGuard.value) return false;
  const t = a.trim();
  if (!t || t.startsWith('tls://') || t.startsWith('https://')) return false;
  let host = t;
  let port = '53';
  const idx = t.lastIndexOf(':');
  if (idx > 0) {
    host = t.slice(0, idx);
    port = t.slice(idx + 1);
  }
  host = host.replace(/^\[/, '').replace(/\]$/, '');
  if (port !== '53') return false;
  if (/^127\./.test(host) || host === '::1' || host === 'localhost') return true;
  return (state.value?.self || []).includes(host);
}

const editRisky = computed(() => (edit.value ?? []).filter(loopRisk));

const editDirty = computed(() => {
  if (edit.value === null) return false;
  return JSON.stringify(edit.value) !== JSON.stringify(ups.value.map((u) => u.address));
});

function startEdit(): void {
  edit.value = ups.value.map((u) => u.address);
}

function addrErr(a: string): string | null {
  const t = a.trim();
  if (!t) return 'пустой адрес';
  const idx = t.lastIndexOf(':');
  if (idx > 0) {
    const port = Number(t.slice(idx + 1));
    if (!Number.isInteger(port) || port < 1 || port > 65535) return 'порт 1-65535';
  }
  return null;
}

const editValid = computed(() => (edit.value ?? []).length > 0 && (edit.value ?? []).every((a) => addrErr(a) === null));

function move(i: number, d: number): void {
  if (!edit.value) return;
  const j = i + d;
  if (j < 0 || j >= edit.value.length) return;
  const c = [...edit.value];
  [c[i], c[j]] = [c[j], c[i]];
  edit.value = c;
}

function removeAt(i: number): void {
  if (!edit.value) return;
  edit.value = edit.value.filter((_, k) => k !== i);
}

function addAddr(): void {
  const a = newAddr.value.trim();
  if (!a) return;
  edit.value = [...(edit.value ?? []), a];
  newAddr.value = '';
}

async function saveEdit(): Promise<void> {
  if (!edit.value || !editValid.value || busy.value) return;
  const risky = editRisky.value;
  const ok = await confirm({
    title: 'Резервные серверы MT',
    text: risky.length
      ? `Осторожно: ${risky.join(', ')} - это служба DNS самого роутера (dnsmasq), а она сейчас направлена в MT. Такой адрес замыкает петлю (dnsmasq → MT → dnsmasq) и уронит DNS. Ставить только если точно понимаете, зачем. ` + 'Остальной список будет записан в настройки MT и заработает без перезапуска.'
      : 'Новый список будет записан в настройки MT и заработает без перезапуска. Первым используется верхний доступный сервер.',
    okLabel: risky.length ? 'Всё равно применить' : 'Сохранить',
  });
  if (!ok) return;
  busy.value = true;
  try {
    await api('PUT', '/dns/upstreams', { upstreams: edit.value, confirm: risky.length > 0 });
    push('список серверов сохранён');
    edit.value = null;
    await Promise.all([load(), loadCascade()]);
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  busy.value = false;
}

// --- правила автопереключения L1-L5: уровни -> порядок серверов MT,
// применение одним запросом в MT; переключает уровни сам MT ---
const policy = ref<DnsPolicyRes | null>(null);
const polEnabled = ref(false);
const polPrimary = ref('');
const polTunnel = ref('');
const polFallback = ref('');
const polYandex = ref(false);
const polProvider = ref(false);
const polBusy = ref(false);
const probeRows = ref<DnsProbeRow[] | null>(null);
const probing = ref(false);

const linesOf = (s: string): string[] => s.split('\n').map((x) => x.trim()).filter(Boolean);

// шифрованные резолверы (MT rev11+): адрес сразу канонический, кнопка
// дописывает строку в основные; на rev10- не показываем
const dohCapable = computed(() => !!policy.value?.dohCapable);
const dohPresets = [
  { label: 'Google DoH', addr: 'https://dns.google' },
  { label: 'Cloudflare DoH', addr: 'https://1.1.1.1/dns-query' },
  { label: 'AdGuard DoH', addr: 'https://94.140.14.14/dns-query' },
  { label: 'Google DoT', addr: 'tls://8.8.8.8' },
  { label: 'Cloudflare DoT', addr: 'tls://1.1.1.1' },
];
function addDohPreset(addr: string): void {
  const cur = linesOf(polPrimary.value);
  if (!cur.includes(addr)) cur.push(addr);
  polPrimary.value = cur.join('\n');
}

async function loadPolicy(): Promise<void> {
  try {
    policy.value = await api<DnsPolicyRes>('GET', '/dns/policy');
    const p = policy.value.policy;
    polEnabled.value = !!p.enabled;
    polPrimary.value = (p.primary || []).join('\n');
    polTunnel.value = p.tunnel || '';
    polFallback.value = (p.fallback || []).join('\n');
    polYandex.value = !!p.yandex;
    polProvider.value = !!p.provider;
  } catch {
    policy.value = null;
  }
}

const tunnelOptions = computed<UiSelectOption[]>(() => [
  { value: '', label: 'напрямую (без туннеля)' },
  ...(statusState.data?.pools || []).map((p) => ({ value: p.name, label: p.name })),
]);

const composedText = computed(() => (policy.value?.composed || []).join(' → '));
const activeText = computed(() => (policy.value?.active || []).join(' → '));

// «Сохранить и применить» доступна только после правок: без изменений
// кнопка недоступна (слово владельца 2026-10-18: «просто так доступна,
// хотя я ничего не правил, это не логично»)
const polDirty = computed(() => {
  const p = policy.value?.policy;
  if (!p) return false;
  return polEnabled.value !== !!p.enabled
    || polPrimary.value !== (p.primary || []).join('\n')
    || polTunnel.value !== (p.tunnel || '')
    || polFallback.value !== (p.fallback || []).join('\n')
    || polYandex.value !== !!p.yandex
    || polProvider.value !== !!p.provider;
});

// рискованные адреса политики: те же уровни петли (L1+L3; L4/L5 - свои)
const polRisky = computed(() => [...linesOf(polPrimary.value), ...linesOf(polFallback.value)].filter(loopRisk));

async function applyPolicy(): Promise<void> {
  const body = {
    enabled: polEnabled.value,
    primary: linesOf(polPrimary.value),
    tunnel: polTunnel.value,
    fallback: linesOf(polFallback.value),
    yandex: polYandex.value,
    provider: polProvider.value,
    confirm: polRisky.value.length > 0,
  };
  if (body.enabled && body.primary.length === 0 && body.fallback.length === 0 && !body.yandex && !body.provider) {
    push('включённая политика требует хотя бы один уровень', true);
    return;
  }
  const ok = await confirm({
    title: 'Сохранить правила автопереключения',
    text: (body.tunnel ? `Для туннельного уровня будет настроено правило обхода через «${body.tunnel}». ` : '')
      + 'Цепочка серверов MT будет переписана: основные → запасные'
      + (body.yandex ? ' → Яндекс' : '')
      + (body.provider ? ' → провайдер' : '')
      + '. Само переключение при сбоях делает MT - автоматически и незаметно.',
    okLabel: 'Сохранить и применить',
  });
  if (!ok) return;
  polBusy.value = true;
  try {
    await api('PUT', '/dns/policy', body);
    push('правила автопереключения применены');
    await Promise.all([load(), loadCascade(), loadPolicy()]);
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  polBusy.value = false;
}

const verdictRu: Record<string, string> = { clean: 'работает', spoof: 'ПОДМЕНА', noanswer: 'нет ответа', error: 'недоступен' };
function verdictVariant(v: string): 'ok' | 'warn' | 'err' {
  return v === 'clean' ? 'ok' : v === 'error' ? 'err' : 'warn';
}

async function probePolicy(): Promise<void> {
  probing.value = true;
  probeRows.value = null;
  try {
    const r = await api<{ results: DnsProbeRow[] }>('POST', '/dns/policy/probe', {});
    probeRows.value = r.results || [];
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  probing.value = false;
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
        <template v-if="isKeen">
          <b>Перехват DNS<UiHelp text="Весь DNS-трафик домашних устройств и самого роутера принудительно заворачивается в MagiTrickle (цепочка MT_DNSOR) - защита работает даже для техники, которой вручную вписан чужой DNS." /></b>
          <span>
            <UiBadge
              :text="state.mt.remap53 ? 'работает' : 'не найдено'"
              :variant="state.mt.remap53 ? 'ok' : 'warn'"
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
          <b>Перенаправление<UiHelp text="Весь DNS-трафик домашних устройств (порт 53) принудительно идёт через модуль MT - защита работает даже для техники, которой вручную вписали чужой DNS." /></b>
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
      <UiHint
        v-if="state && isKeen"
        text="Собственные серверы Keenetic панель не меняет - управляемая часть одна: резервные серверы MagiTrickle ниже."
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

      <!-- резервные серверы MT: живой статус (форк rev8+) -->
      <template v-if="capable">
        <div class="subhead">
          <b>Резервные серверы<UiHelp text="Внешние DNS-серверы в порядке приоритета: запросы идут в первый доступный, при его сбое - в следующий по списку. Доступность проверяется контрольным запросом к deb.torproject.org: честный сервер возвращает известный набор адресов, подменённый виден сразу и помечается «не работает»." /></b>
          <span class="muted">{{ healthText }}</span>
        </div>
        <div class="kv">
          <template
            v-for="(u, i) in ups"
            :key="u.address"
          >
            <b>{{ i + 1 }}.</b>
            <span>
              <UiBadge
                :text="u.address"
                :variant="u.alive ? 'ok' : 'err'"
                :tooltip="upTooltip(u)"
              />
              <UiBadge
                v-if="u.active"
                variant="info"
                text="работает сейчас"
                tooltip="первый доступный - запросы идут в него в первую очередь"
              />
              <UiBadge
                v-if="!u.alive"
                variant="err"
                text="не работает"
                :tooltip="upTooltip(u)"
              />
            </span>
          </template>
        </div>
        <UiHint text="Автопроверка: контрольный запрос к deb.torproject.org; честный сервер возвращает известный набор адресов, подменённый - виден сразу и помечается «не работает»." />

        <!-- редактор: список/порядок/добавление -->
        <div
          v-if="edit === null"
          class="dnsactions"
        >
          <UiButton
            :disabled="busy"
            @click="startEdit"
          >
            Редактировать список
          </UiButton>
        </div>
        <template v-else>
          <div class="editrows">
            <div
              v-for="(a, i) in editList"
              :key="i"
              class="editrow"
            >
              <span class="idx">{{ i + 1 }}</span>
              <code
                class="addr"
                :class="{ bad: addrErr(a) !== null }"
                :title="addrErr(a) ?? ''"
              >{{ a }}</code>
              <UiBadge
                v-if="loopRisk(a)"
                variant="warn"
                text="петля?"
                tooltip="Это адрес службы DNS самого роутера (dnsmasq), а она сейчас направлена в MT: такой адрес замкнёт петлю и уронит DNS. На Кинетике такой адрес легален, на OpenWrt - нет."
              />
              <span class="rowbtns">
                <UiButton
                  :disabled="i === 0"
                  @click="move(i, -1)"
                >
                  ↑
                </UiButton>
                <UiButton
                  :disabled="i === editList.length - 1"
                  @click="move(i, 1)"
                >
                  ↓
                </UiButton>
                <UiButton
                  :disabled="busy"
                  @click="removeAt(i)"
                >
                  ✕
                </UiButton>
              </span>
            </div>
          </div>
          <div class="dnsactions">
            <UiInput
              v-model="newAddr"
              bare
              mono
              placeholder="8.8.8.8 или 8.8.8.8:5353"
            />
            <UiButton
              :disabled="!newAddr.trim()"
              @click="addAddr"
            >
              Добавить
            </UiButton>
          </div>
          <div class="dnsactions">
            <UiButton
              :busy="busy"
              :disabled="!editValid || !editDirty"
              @click="saveEdit"
            >
              Сохранить
            </UiButton>
            <UiButton
              :disabled="busy"
              @click="edit = null"
            >
              Отмена
            </UiButton>
            <UiBadge
              v-if="!editValid"
              variant="err"
              text="исправьте адреса (порт 1-65535)"
            />
            <UiBadge
              v-if="editRisky.length"
              variant="warn"
              text="есть адрес dnsmasq - при сохранении спросит подтверждение"
              tooltip="Адрес службы DNS роутера при dnsmasq «в MT» замыкает петлю. Поставить можно только осознанно."
            />
          </div>
        </template>
        <UiHint text="Правка записывается в настройки MT и заработает без перезапуска. Порядок в списке = приоритет." />
      </template>
      <UiHint
        v-else-if="state"
        text="Живой статус и правка списка требуют MagiTrickle 0.8.2-rev8+; показан список серверов из файла настроек."
      />

      <div class="dnsactions">
        <UiButton
          v-if="!isKeen"
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

  <UiCard
    id="card-dns-policy"
    title="Правила автопереключения"
    subtitle="уровни L1–L5: серверы проверяются по порядку, при сбое - переход на следующий"
  >
    <template #actions>
      <UiBadge
        v-if="policy"
        variant="ok"
        text="L1-L5"
      />
    </template>

    <UiHint
      v-if="!policy || !policy.capable"
      text="Для автопереключения нужен MagiTrickle 0.8.2-rev8+ (список серверов с автопроверкой). Обновите MT - и настройте уровни здесь."
    />
    <template v-else>
      <div class="hrow">
        <UiSwitch
          v-model="polEnabled"
          label="Автопереключение включено"
          tooltip="выключенная политика только сохраняется - текущие серверы не трогаются"
        />
        <UiHelp text="Роутер держит цепочку DNS-серверов: от основных (уровень 1) до последнего рубежа (уровень 5). Если очередной сервер не отвечает или врёт, запросы автоматически уходят на следующий; когда основной «оживёт», роутер вернётся к нему - без вашего участия." />
      </div>
      <div class="hrow">
        <UiTextarea
          v-model="polPrimary"
          label="Уровень 1 (L1) - основные серверы"
          :rows="2"
          mono
          placeholder="8.8.8.8&#10;tls://1.1.1.1 · https://dns.google"
        />
        <UiHelp text="Первые в цепочке - их роутер пробует в первую очередь. По одному адресу на строку; порт можно не указывать (подставится стандартный 53). Шифрованные серверы: DoT - tls://адрес (порт 853), DoH - https://адрес/dns-query (порт 443) - требуют MT 0.8.2-rev11+." />
      </div>
      <div
        v-if="dohCapable"
        class="hrow"
      >
        <span class="muted">Добавить шифрованный:</span>
        <button
          v-for="p in dohPresets"
          :key="p.addr"
          type="button"
          class="dohpreset"
          @click="addDohPreset(p.addr)"
        >
          {{ p.label }}
        </button>
        <UiHelp text="Кнопки дописывают готовый адрес в основные серверы. DoH/DoT - DNS поверх TLS: подменить или подсмотреть ответы на пути нельзя; сами запросы к серверу можно пустить через туннель (уровень L2)." />
      </div>
      <UiSelect
        v-model="polTunnel"
        :options="tunnelOptions"
        label="Уровень 2 (L2) - основные через туннель"
      />
      <UiHint text="Те же основные серверы, но запросы к ним пойдут внутри выбранного VPN-подключения - так DNS не заблокировать. Пусто - основные ходят напрямую." />
      <div class="hrow">
        <UiTextarea
          v-model="polFallback"
          label="Уровень 3 (L3) - запасные серверы"
          :rows="2"
          mono
          placeholder="1.1.1.1:53"
        />
        <UiHelp text="Независимые серверы в запас: включаются, когда основные недоступны и напрямую, и через туннель. Формат адресов тот же, включая tls:// и https://." />
      </div>
      <div class="hrow">
        <UiSwitch
          v-model="polYandex"
          label="Уровень 4 (L4) - Яндекс DNS (77.88.8.8)"
        />
        <UiHelp text="Надёжный публичный сервер, доступный напрямую: хороший последний рубеж - если он работает, DNS в порядке." />
      </div>
      <div class="hrow">
        <UiSwitch
          v-model="polProvider"
          label="Уровень 5 (L5) - DNS провайдера (определяется автоматически)"
        />
        <UiHelp text="Адрес сервера провайдера берётся из системных настроек роутера в момент сохранения. Если провайдер сменит его - примените правила заново." />
      </div>

      <div class="kv">
        <template v-if="composedText">
          <b>Цепочка серверов</b>
          <span class="muted">{{ composedText }}</span>
        </template>
        <template v-if="activeText">
          <b>Активно сейчас</b>
          <span class="muted">{{ activeText }}</span>
        </template>
        <template v-if="policy.tunnelGroup">
          <b>Туннельный обход<UiHelp text="Правило dns-tunnel: запросы к публичным резолверам (8.8.8.8, 1.1.1.1 и др.) уходят внутри туннеля выбранного подключения." /></b>
          <span class="muted">настроен через {{ policy.tunnelGroup.interface }}</span>
        </template>
      </div>

      <UiBanner
        v-if="polRisky.length"
        kind="warn"
        text="В уровнях есть адрес службы DNS самого роутера (dnsmasq), а она сейчас направлена в MT - такой адрес замкнёт петлю и уронит DNS. Сохранить можно только осознанно: панель спросит подтверждение."
      />

      <!-- результаты пробы - над кнопками, чтобы не сдвигали их вниз -->
      <div
        v-if="probeRows"
        class="kv"
      >
        <template
          v-for="r in probeRows"
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

      <div class="dnsactions">
        <UiButton
          :busy="polBusy"
          :disabled="!polDirty"
          tooltip="становится доступной после правки любого уровня"
          @click="applyPolicy"
        >
          Сохранить и применить
        </UiButton>
        <UiButton
          :busy="probing"
          :disabled="!polEnabled"
          @click="probePolicy"
        >
          Проверить все уровни
        </UiButton>
      </div>

      <div class="hrow">
        <UiHint text="Как работает проверка: роутер отправляет контрольный запрос к deb.torproject.org через каждый уровень - два прогона, первый греет туннель, в итог идёт лучший из двух. Если сервер не отвечает или подменяет ответ - он помечается нерабочим, запросы уходят на следующий уровень." />
        <UiHelp text="Почему домен именно этот: его подмена видна по самому ответу - сравнивается с эталонным набором адресов. «Сервер отвечает» и «серверу можно верить» - не одно и то же: на сетях с блокировками сервер может бодро отдавать неверные адреса." />
      </div>
    </template>
  </UiCard>
</template>

<style scoped>
.dohpreset {
  border: 1px solid var(--border, #3a3f4a);
  background: transparent;
  color: inherit;
  border-radius: 6px;
  padding: 2px 8px;
  font-size: 12px;
  cursor: pointer;
}
.dohpreset:hover {
  border-color: var(--accent, #4a8fd4);
}
.dnsactions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 10px;
  align-items: center;
}
.subhead {
  display: flex;
  gap: 10px;
  align-items: baseline;
  flex-wrap: wrap;
  margin-top: 12px;
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
.editrows {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-top: 10px;
}
.editrow {
  display: flex;
  gap: 8px;
  align-items: center;
}
.editrow .idx {
  width: 18px;
  text-align: right;
  color: var(--dim);
}
.editrow .addr {
  flex: 1;
  min-width: 0;
  overflow-wrap: anywhere;
}
.editrow .addr.bad {
  color: var(--err);
}
.editrow .rowbtns {
  display: flex;
  gap: 4px;
}
</style>
