<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { UiBadge, UiButton, UiHelp, UiHint, UiInput, UiSwitch } from '../../components/ui';
import { api } from '../../api/client';
import type { DnsState, DnsUpstream, DnsUpstreamsRes } from '../../api/types';
import {
  addrErr, editConfirmAsk, editDirtyOf, editValidOf, healthDraftOf, healthDirtyOf,
  healthPutBody, healthTextOf, healthValidOf, loopGuardOf, loopRiskOf, swappedAt,
  upTooltipOf, upstreamsPutBody, type HealthDraft,
} from './dnsServers';
import { G } from '../glossary';
import { useToast } from '../../stores/toast';
import { useConfirm } from '../../stores/confirm';

// Секция «Резервные серверы MT» (каскад rev8+): живой статус, политика
// автопроверки (rev14+) и редактор списка. Cascade-стейт опрашивается
// родителем (10с) и приходит пропсом; busy - общий с карточкой.
const props = defineProps<{
  state: DnsState | null;
  cascade: DnsUpstreamsRes | null;
  busy: boolean;
  // маршрутизация DNS выключена (115-док): редакторы гасятся, живой статус
  // и пробы остаются - канарейки работают без перехвата
  routingOff?: boolean;
  reload: () => Promise<void>;
  reloadAll: () => Promise<void>;
}>();
const emit = defineEmits<{ busy: [v: boolean] }>();

const { push } = useToast();
const { confirm } = useConfirm();

const capable = computed(() => props.cascade?.capable === true && !!props.cascade.state);
const ups = computed(() => props.cascade?.state?.upstreams ?? []);
const health = computed(() => props.cascade?.state?.health);

const healthText = computed(() => healthTextOf(health.value));

function upTooltip(u: DnsUpstream): string {
  return upTooltipOf(u);
}

// политика автопроверки серверов (форк rev14+, 82-док): правка уходит в MT
// health-only PUT-ом - список серверов не трогается, петлезащита не спрашивается
const healthCapable = computed(() => props.cascade?.healthCapable === true);

const hEnabled = ref(true);
const hInterval = ref('');
const hTimeout = ref('');
const hFail = ref('');
const hRestore = ref('');
const hGap = ref('');
const healthSaving = ref(false);
// черновик ещё не синхронизирован с сервером (первая загрузка / после
// сохранения): пустые поля - не правка пользователя, а «ещё не заполнено»
const hSynced = ref(false);

const hDraft = (): HealthDraft => ({
  enabled: hEnabled.value,
  interval: hInterval.value,
  timeout: hTimeout.value,
  fail: hFail.value,
  restore: hRestore.value,
  gap: hGap.value,
});

const healthDirty = computed(() => healthDirtyOf(health.value, hDraft()));

const healthValid = computed(() => healthValidOf(hDraft()));

watch(health, (h) => {
  if (!h || hSynced.value) return;
  hSynced.value = true;
  const d = healthDraftOf(h);
  hEnabled.value = d.enabled;
  hInterval.value = d.interval;
  hTimeout.value = d.timeout;
  hFail.value = d.fail;
  hRestore.value = d.restore;
  hGap.value = d.gap;
}, { immediate: true });

async function saveHealth(): Promise<void> {
  if (!healthValid.value || !healthDirty.value || healthSaving.value) return;
  healthSaving.value = true;
  emit('busy', true);
  try {
    await api('PUT', '/dns/upstreams', healthPutBody(hDraft()));
    push('настройки проверки сохранены');
    hSynced.value = false; // после сохранения черновик заново сверится с сервером
    await props.reload();
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  healthSaving.value = false;
  emit('busy', false);
}

// редактор списка серверов MT: порядок = приоритет
const edit = ref<string[] | null>(null);
const newAddr = ref('');
const editList = computed(() => edit.value ?? []);

const loopGuard = computed(() => loopGuardOf(props.state));

function loopRisk(a: string): boolean {
  return loopRiskOf(a, loopGuard.value, props.state?.self || []);
}

const editRisky = computed(() => (edit.value ?? []).filter(loopRisk));

const editDirty = computed(() => editDirtyOf(edit.value, ups.value.map((u) => u.address)));
const editValid = computed(() => editValidOf(edit.value));

function startEdit(): void {
  edit.value = ups.value.map((u) => u.address);
}

function move(i: number, d: number): void {
  if (!edit.value) return;
  const c = swappedAt(edit.value, i, d);
  if (c) edit.value = c;
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
  if (!edit.value || !editValid.value || props.busy) return;
  const risky = editRisky.value;
  const ask = editConfirmAsk(risky);
  const ok = await confirm(ask);
  if (!ok) return;
  emit('busy', true);
  try {
    await api('PUT', '/dns/upstreams', upstreamsPutBody(edit.value, risky.length));
    push('список серверов сохранён');
    edit.value = null;
    await props.reloadAll();
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  emit('busy', false);
}
</script>

<template>
  <!-- резервные серверы MT: живой статус (форк rev8+) -->
  <template v-if="capable">
    <div class="subhead">
      <b>Резервные серверы<UiHelp text="DNS-серверы, через которые резолвят домашние устройства при перехвате MagiTrickle (dns-прокси MT), в порядке приоритета: запросы идут в первый доступный, при его сбое - в следующий по списку. Доступность проверяется контрольным запросом к deb.torproject.org: честный сервер возвращает известный набор адресов, подменённый виден сразу и помечается «не работает». Локальные записи роутера (ip host) через эти серверы не резолвятся - их знает только DNS Keenetic." /></b>
      <span class="muted">{{ healthText }}</span>
    </div>
    <UiHint
      v-if="routingOff"
      text="Маршрутизация DNS выключена: на запросы отвечает резолвер роутера, список приостановлен и вернется при включении вверху вкладки. Живой статус остаётся виден."
    />
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

    <!-- политика автопроверки (форк rev14+, 82-док): каждое поле -
         с минусами в обе стороны; в режиме «не лезть» гасится (115-док) -->
    <template v-if="healthCapable && !routingOff">
      <div class="cycles">
        <UiSwitch
          v-model="hEnabled"
          label="Автопроверка серверов"
          tooltip="контрольный запрос к каждому серверу по очереди. Выключить - процессор свободнее, но упавший сервер никто не заметит: запросы продолжат уходить в мёртвый."
        />
        <UiHelp :text="G.dnsHealth" />
      </div>
      <div class="cycles">
        <span class="muted">проверять каждые</span>
        <UiInput
          v-model="hInterval"
          type="number"
          bare
          class="cnum"
          placeholder="10"
          :disabled="!hEnabled"
          tooltip="как часто проверять серверы. Чаще - быстрее замечаем смерть сервера, но каждое срабатывание даёт всплеск нагрузки на процессор (проверка шифрованных серверов - это TLS-рукопожатие). Реже - процессор свободнее, но проблемы видны за интервал × циклы."
        />
        <span class="muted">сек, ждать ответа</span>
        <UiInput
          v-model="hTimeout"
          type="number"
          bare
          class="cnum"
          placeholder="2"
          :disabled="!hEnabled"
          tooltip="сколько ждать ответа сервера. Меньше - быстрее вердикт, но медленный сервер ложно пометится нерабочим (ложные переключения). Больше - надёжный вердикт, но каждая проверка дольше занимает процессор."
        />
        <span class="muted">сек, отключать после</span>
        <UiInput
          v-model="hFail"
          type="number"
          bare
          class="cnum"
          placeholder="3"
          :disabled="!hEnabled"
          tooltip="сколько провальных проверок подряд считается смертью сервера. Меньше - быстрая реакция, но флап на одной потере; больше - стабильно, но реально мёртвый сервер работает лишние циклы, пока его заметят."
        />
        <span class="muted">сбоев, возвращать после</span>
        <UiInput
          v-model="hRestore"
          type="number"
          bare
          class="cnum"
          placeholder="2"
          :disabled="!hEnabled"
          tooltip="защита от дёрганья: снова живой сервер возвращается только после N удачных проверок. Меньше - быстрый возврат, но флапающий сервер будет дёргать маршруты; больше - стабильно, но резерв надолго остаётся главным."
        />
        <span class="muted">успешных и не чаще раза в</span>
        <UiInput
          v-model="hGap"
          type="number"
          bare
          class="cnum"
          placeholder="300"
          :disabled="!hEnabled"
          tooltip="минимальная пауза между переключениями, сек. Меньше (вплоть до 0) - сервер возвращается сразу; больше - стабильно, но упавший основной дольше остаётся главным."
        />
        <span class="muted">сек</span>
        <UiButton
          :disabled="!healthDirty || !healthValid"
          :busy="healthSaving"
          @click="saveHealth"
        >
          сохранить
        </UiButton>
        <UiBadge
          v-if="!healthValid"
          variant="err"
          text="вне диапазона"
          tooltip="интервал 5-300 сек, таймаут 1-10 сек, циклы 1-20, пауза возврата 0-3600 сек"
        />
      </div>
    </template>

    <div
      v-if="edit === null && !routingOff"
      class="dnsactions"
    >
      <UiButton
        :disabled="busy"
        @click="startEdit"
      >
        Редактировать список
      </UiButton>
    </div>
    <template v-else-if="!routingOff">
      <UiHint text="Список - порядок, в котором MagiTrickle отвечает на DNS-запросы устройств: верхний - главный, при его сбое запросы идут вниз. Обычно здесь внешние серверы (8.8.8.8, DoH). Адрес резолвера роутера (127.0.0.1) допустим, только когда служба DNS роутера НЕ направлена в MT - иначе петля, панель предупредит бейджем." />
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
            text="риск петли"
            tooltip="Это адрес резолвера самого роутера (dnsmasq/ndm). Сейчас служба DNS роутера направлена в MT - такой адрес зациклит её (dnsmasq → MT → dnsmasq) и уронит DNS устройств. Ставить только если понимаете зачем: сейчас этот адрес уже отвечает через MT, список обычно нужен с внешними серверами."
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
.cycles {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 12px;
  font-size: 12px;
}
.cnum {
  width: 64px;
}
</style>
