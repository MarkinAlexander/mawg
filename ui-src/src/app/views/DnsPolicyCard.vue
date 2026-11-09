<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { UiBadge, UiBanner, UiButton, UiCard, UiHelp, UiHint, UiSelect, type UiSelectOption, UiSwitch, UiTextarea } from '../../components/ui';
import { api } from '../../api/client';
import type { DnsPolicyRes, DnsState, DnsProbeRow } from '../../api/types';
import { dohPresets, policyBodyOf, policyConfirmAsk, policyDirtyOf, policyDraftOf, policyMissingLevels, withPreset, type PolicyDraft } from './dnsPolicy';
import { linesOf, verdictRu, verdictVariant } from './dnsLogic';
import { loopGuardOf, loopRiskOf } from './dnsServers';
import { useStatus } from '../../stores/status';
import { useToast } from '../../stores/toast';
import { useConfirm } from '../../stores/confirm';

// правила автопереключения L1-L5: уровни -> порядок серверов MT,
// применение одним запросом; переключает уровни сам MT
const props = defineProps<{
  state: DnsState | null;
  // режим «mawg не лезет» (115-док): правка политики гасится, пробы живут
  managedOff?: boolean;
  reloadAll: () => Promise<void>;
}>();

const { push } = useToast();
const { confirm } = useConfirm();
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

async function loadPolicy(): Promise<void> {
  try {
    policy.value = await api<DnsPolicyRes>('GET', '/dns/policy');
    const d = policyDraftOf(policy.value.policy);
    polEnabled.value = d.enabled;
    polPrimary.value = d.primary;
    polTunnel.value = d.tunnel;
    polFallback.value = d.fallback;
    polYandex.value = d.yandex;
    polProvider.value = d.provider;
  } catch {
    policy.value = null;
  }
}
onMounted(loadPolicy);

const draft = (): PolicyDraft => ({
  enabled: polEnabled.value,
  primary: polPrimary.value,
  tunnel: polTunnel.value,
  fallback: polFallback.value,
  yandex: polYandex.value,
  provider: polProvider.value,
});

// шифрованные резолверы (MT rev11+): адрес сразу канонический, кнопка
// дописывает строку в основные; на rev10- не показываем
const dohCapable = computed(() => !!policy.value?.dohCapable);

function addDohPreset(addr: string): void {
  polPrimary.value = withPreset(polPrimary.value, addr);
}

const tunnelOptions = computed<UiSelectOption[]>(() => [
  { value: '', label: 'напрямую (без туннеля)' },
  ...(statusState.data?.pools || []).map((p) => ({ value: p.name, label: p.name })),
]);

const composedText = computed(() => (policy.value?.composed || []).join(' → '));
const activeText = computed(() => (policy.value?.active || []).join(' → '));

const polDirty = computed(() => policyDirtyOf(policy.value?.policy, draft()));

const loopGuard = computed(() => loopGuardOf(props.state));

function loopRisk(a: string): boolean {
  return loopRiskOf(a, loopGuard.value, props.state?.self || []);
}

// рискованные адреса политики: те же уровни петли (L1+L3; L4/L5 - свои)
const polRisky = computed(() => [...linesOf(polPrimary.value), ...linesOf(polFallback.value)].filter(loopRisk));

async function applyPolicy(): Promise<void> {
  const body = policyBodyOf(draft(), polRisky.value.length);
  if (policyMissingLevels(body)) {
    push('включённая политика требует хотя бы один уровень', true);
    return;
  }
  const ok = await confirm(policyConfirmAsk(body));
  if (!ok) return;
  polBusy.value = true;
  try {
    await api('PUT', '/dns/policy', body);
    push('правила автопереключения применены');
    await Promise.all([props.reloadAll(), loadPolicy()]);
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  polBusy.value = false;
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
      <UiHint
        v-if="managedOff"
        text="Режим «mawg не лезет»: правка политики отключена - включите «DNS управляется mawg» вверху вкладки. Проверка уровней ниже работает."
      />
      <div class="hrow">
        <UiSwitch
          v-model="polEnabled"
          :disabled="managedOff"
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
          :disabled="!polDirty || managedOff"
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
</style>
