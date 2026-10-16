<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { UiBadge, UiButton, UiCard, UiHelp, UiHint, UiInput, UiSelect, type UiSelectOption, UiSwitch, UiTextarea } from '../../components/ui';
import { api } from '../../api/client';
import type { DnsProbeRow, DnsState, DnsPolicyRes, DnsUpstream, DnsUpstreamsRes } from '../../api/types';
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
  if (d.servers.length) return `свои серверы: ${d.servers.join(', ')}`;
  return 'напрямую (серверы провайдера)';
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
    title: 'Резервные серверы MT',
    text: 'Новый список будет записан в настройки MT и заработает без перезапуска. Первым используется верхний доступный сервер.',
    okLabel: 'Сохранить',
  });
  if (!ok) return;
  busy.value = true;
  try {
    await api('PUT', '/dns/upstreams', { upstreams: edit.value });
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
        v-if="state"
        variant="ok"
        text="openwrt"
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
          placeholder="8.8.8.8"
        />
        <UiHelp text="Первые в цепочке - их роутер пробует в первую очередь. По одному адресу на строку; порт можно не указывать (подставится стандартный 53)." />
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
        <UiHelp text="Независимые серверы в запас: включаются, когда основные недоступны и напрямую, и через туннель." />
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

      <div class="dnsactions">
        <UiButton
          :busy="polBusy"
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

      <div class="hrow">
        <UiHint text="Как работает проверка: роутер отправляет контрольный запрос к deb.torproject.org через каждый уровень. Если сервер не отвечает или подменяет ответ - он помечается нерабочим, запросы уходят на следующий уровень." />
        <UiHelp text="Почему домен именно этот: его подмена видна по самому ответу - сравнивается с эталонным набором адресов. «Сервер отвечает» и «серверу можно верить» - не одно и то же: на сетях с блокировками сервер может бодро отдавать неверные адреса." />
      </div>
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
