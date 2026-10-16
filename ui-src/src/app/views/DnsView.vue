<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { UiBadge, UiButton, UiCard, UiHint, UiInput, UiSelect, type UiSelectOption, UiSwitch, UiTextarea } from '../../components/ui';
import { api } from '../../api/client';
import type { DnsProbeRow, DnsState, DnsPolicyRes, DnsUpstream, DnsUpstreamsRes } from '../../api/types';
import { useStatus } from '../../stores/status';
import { useToast } from '../../stores/toast';
import { useConfirm } from '../../stores/confirm';

// DNS-раздел (46-док §3, срез OpenWrt 64-док): состояние DNS-стека
// (dnsmasq, remap53) + переключение dnsmasq ↔ MT «живучим» профилем;
// каскад апстримов MT - живой стейт и редактор через форк rev8+
// (GET/PUT /dns/upstreams, стейт в памяти MT, правка = запись конфига
// + горячая подмена без рестарта демона).
const { push } = useToast();
const { confirm } = useConfirm();

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

const capable = computed(() => cascade.value?.capable === true && !!cascade.value.state);
const ups = computed(() => cascade.value?.state?.upstreams ?? []);
const health = computed(() => cascade.value?.state?.health);

const healthText = computed(() => {
  const h = health.value;
  if (!h) return '';
  if (!h.enabled) return 'канарейка выключена';
  return `канарейка: вниз за ${h.failCycles} сбоя подряд, вверх за ${h.restoreCycles} чистых, возврат не чаще раза в ${h.restoreGap}`;
});

function upTooltip(u: DnsUpstream): string {
  const bits: string[] = [];
  if (u.reason) bits.push('причина: ' + u.reason);
  if (u.lastSwap && !u.lastSwap.startsWith('0001-')) bits.push('переключён: ' + new Date(u.lastSwap).toLocaleString());
  bits.push(`сбои подряд: ${u.fails}, чистые подряд: ${u.oks}`);
  return bits.join(' · ');
}

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

// --- редактор каскада (порядок = приоритет) ---
const edit = ref<string[] | null>(null);
const newAddr = ref('');
const editList = computed(() => edit.value ?? []);

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
  const ok = await confirm({
    title: 'Каскад апстримов MT',
    text: 'Новый список будет записан в config.yaml MT и подхвачен без рестарта демона. Порядок = приоритет: клиенты идут в первый живой апстрим.',
    okLabel: 'Сохранить',
  });
  if (!ok) return;
  busy.value = true;
  try {
    await api('PUT', '/dns/upstreams', { upstreams: edit.value });
    push('каскад апстримов сохранён');
    edit.value = null;
    await Promise.all([load(), loadCascade()]);
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  busy.value = false;
}

// --- политика каскада L1-L5 (46-док §3): уровни -> порядок апстримов MT,
// применение одним PUT в форк; гистерезис переключений - в MT ---
const { state: statusState } = useStatus();

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

async function applyPolicy(): Promise<void> {
  const body = {
    enabled: polEnabled.value,
    primary: linesOf(polPrimary.value),
    tunnel: polTunnel.value,
    fallback: linesOf(polFallback.value),
    yandex: polYandex.value,
    provider: polProvider.value,
  };
  if (body.enabled && body.primary.length === 0 && body.fallback.length === 0 && !body.yandex && !body.provider) {
    push('включённая политика требует хотя бы один уровень', true);
    return;
  }
  const ok = await confirm({
    title: 'Применить политику каскада',
    text: (body.tunnel ? `Пул ${body.tunnel} получит пресет dns-tunnel (публичные резолверы - в туннель). ` : '')
      + 'Порядок апстримов MT будет переписан: основные → запасные'
      + (body.yandex ? ' → Яндекс' : '')
      + (body.provider ? ' → провайдер' : '')
      + '. Переключает уровни сам MT (канарейка, K/M/restoreGap).',
    okLabel: 'Применить',
  });
  if (!ok) return;
  polBusy.value = true;
  try {
    await api('PUT', '/dns/policy', body);
    push('политика каскада применена');
    await Promise.all([load(), loadCascade(), loadPolicy()]);
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  polBusy.value = false;
}

const verdictRu: Record<string, string> = { clean: 'чисто', spoof: 'ПОДМЕНА', noanswer: 'нет ответа', error: 'недоступен' };
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
    subtitle="состояние DNS-стека роутера, каскад апстримов MT и переключение dnsmasq"
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
        <template v-if="!capable">
          <b>апстримы MT</b>
          <span class="muted">{{ upstreamsText }}</span>
        </template>
      </div>

      <!-- каскад апстримов MT: живой стейт (форк rev8+) -->
      <template v-if="capable">
        <div class="subhead">
          <b>каскад апстримов MT</b>
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
                text="активный"
                tooltip="первый живой - клиентские запросы идут в него в первую очередь"
              />
              <UiBadge
                v-if="!u.alive"
                variant="err"
                text="мёртвый"
                :tooltip="upTooltip(u)"
              />
            </span>
          </template>
        </div>

        <!-- редактор: список/порядок/добавление -->
        <div
          v-if="edit === null"
          class="dnsactions"
        >
          <UiButton
            :disabled="busy"
            @click="startEdit"
          >
            Редактировать каскад
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
          </div>
        </template>
        <UiHint text="Порядок = приоритет: запросы идут в первый живой апстрим (канарейка deb.torproject.org решает, кто отравлен; вниз за K сбоев подряд, вверх за M чистых и не чаще раза в restoreGap). Правка пишется в config.yaml MT и подхватывается без рестарта демона." />
      </template>
      <UiHint
        v-else-if="state"
        text="Живой статус и редактор каскада требуют MagiTrickle 0.8.2-rev8+; показан список апстримов из config.yaml."
      />

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

      <UiHint text="«Живучий» профиль: dnsmasq держит MT:3553 параллельно интерфейсному upstream - смерть MT даёт деградацию до прямого DNS, не смерть (63-док)." />
    </template>
  </UiCard>

  <UiCard
    id="card-dns-policy"
    title="Политика каскада"
    subtitle="уровни L1-L5 → порядок апстримов MT; переключает уровни сам MT (канарейка)"
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
      text="Политика требует MagiTrickle 0.8.2-rev8+ (мультиапстрим с канарейкой). Обновите MT - и настройте уровни здесь."
    />
    <template v-else>
      <UiSwitch
        v-model="polEnabled"
        label="политика включена"
        tooltip="уровни применяются в порядок апстримов MT; выключение не трогает текущие апстримы"
      />
      <UiTextarea
        v-model="polPrimary"
        label="L1 основные (по одному адресу в строке)"
        :rows="2"
        mono
        placeholder="8.8.8.8"
      />
      <UiSelect
        v-model="polTunnel"
        :options="tunnelOptions"
        label="L2: основные через туннель (пул)"
      />
      <UiHint text="Выбранный пул получит пресет dns-tunnel (публичные резолверы маршрутизируются в туннель group-правилами). Пусто - основные напрямую; существующая группа dns-tunnel при смене пула переезжает." />
      <UiTextarea
        v-model="polFallback"
        label="L3 запасные (напрямую)"
        :rows="2"
        mono
        placeholder="1.1.1.1:53"
      />
      <div class="polswitches">
        <UiSwitch
          v-model="polYandex"
          label="L4: Яндекс DNS (77.88.8.8)"
        />
        <UiSwitch
          v-model="polProvider"
          label="L5: DNS провайдера (срез resolv.conf.auto при применении)"
        />
      </div>

      <div class="kv">
        <template v-if="composedText">
          <b>порядок</b>
          <span class="muted">{{ composedText }}</span>
        </template>
        <template v-if="activeText">
          <b>сейчас в MT</b>
          <span class="muted">{{ activeText }}</span>
        </template>
        <template v-if="policy.tunnelGroup">
          <b>dns-tunnel</b>
          <span class="muted">на пуле {{ policy.tunnelGroup.interface }}</span>
        </template>
      </div>

      <div class="dnsactions">
        <UiButton
          :busy="polBusy"
          @click="applyPolicy"
        >
          Применить политику
        </UiButton>
        <UiButton
          :busy="probing"
          :disabled="!polEnabled"
          @click="probePolicy"
        >
          Проверить уровни
        </UiButton>
      </div>

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

      <UiHint text="Канарейка - A-запрос deb.torproject.org через каждый уровень. Туннельные уровни зондируются по-настоящему: трафик самой панели маркируется группой MT и уходит в туннель. Смена уровня - работа MT: вниз за K сбоев подряд, вверх за M чистых и не чаще раза в restoreGap." />
    </template>
  </UiCard>
</template>

<style scoped>
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
.polswitches {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 4px 0;
}
</style>
