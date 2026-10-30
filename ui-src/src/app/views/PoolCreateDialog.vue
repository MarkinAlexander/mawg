<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { UiBadge, UiBanner, UiButton, UiDialog, UiHelp, UiHint, UiInput, UiSelect, type UiSelectOption, UiTextarea } from '../../components/ui'
import { useStatus } from '../../stores/status'
import { usePoolActions } from '../../stores/pools'
import { useCaptcha } from '../../stores/captcha'
import { useToast } from '../../stores/toast'
import type { LinkInspect, SourcePlan } from '../../api/types'
import type { CreatePoolBody } from '../../stores/pools'
import { inspectLine, planView, protoGate, slotGate, slotOptions } from './poolsLogic'
import LxInstallDialog from './LxInstallDialog.vue'
import { addrSecretIn } from '../secrets'
import { G } from '../glossary'

// Диалог создания пула (легаси dlgPool/setPoolMode 404-805). Режимы:
// конфиги | ссылка или подписка | Amnezia Free. Слоты - только Keenetic,
// протокол - только OpenWrt. Капча Free - встроенной фазой диалога.
const open = defineModel<boolean>('open', { default: false });

const { state: status, refresh: refreshStatus } = useStatus();
const a = usePoolActions();
const { push } = useToast();

type Mode = 'cfg' | 'link' | 'free';
const mode = ref<Mode>('cfg');
const name = ref('');
const fallback = ref('direct');
const slot = ref('');
const proto = ref('wireguard');
const source = ref('');
const busy = ref(false);
const inspecting = ref(false);
const inspectRes = ref<LinkInspect | null>(null);
const inspectErr = ref('');
const slotsLoading = ref(false);
const slotsErr = ref('');
const slotList = ref<{ value: string; label: string; busy: boolean }[]>([]);
const amneziaVia = ref('');
const plan = ref<SourcePlan | null>(null);
const done = ref(false);

const isKeenetic = computed(() => status.data?.platform === 'keenetic');
const platform = computed(() => status.data?.platform || '');

watch(open, (v) => {
  if (v) {
    mode.value = 'cfg';
    name.value = '';
    fallback.value = 'direct';
    slot.value = '';
    proto.value = 'wireguard';
    source.value = '';
    inspectRes.value = null;
    inspectErr.value = '';
    plan.value = null;
    done.value = false;
    files.value = [];
    cfgEngine.value = '';
    awg3Sniffed.value = false;
    awg3Files.value = 0;
    void loadSlots();
  }
});

const modeOptions: { value: Mode; label: string }[] = [
  { value: 'cfg', label: 'конфиги .conf / .zip' },
  { value: 'link', label: 'ссылка или подписка' },
  { value: 'free', label: 'Amnezia Free без аккаунта' },
];
function setMode(m: Mode): void {
  mode.value = m;
  if (m === 'free') proto.value = 'amneziawg';
  engineCore.value = '';
  inspectRes.value = null;
  inspectErr.value = '';
  plan.value = null;
  done.value = false;
}

const slotPickOptions = computed<UiSelectOption[]>(() => slotList.value.map((s) => ({ value: s.value, label: s.label })));
const gate = computed(() => {
  const picked = slotList.value.find((s) => s.value === slot.value);
  return slotGate(mode.value, inspectRes.value, !!picked?.busy);
});
const showInspect = computed(() => mode.value === 'link' && (!!source.value.trim() || inspecting.value));
// селект «Протокол (OpenWrt)» ведёт разбор ссылки: движковые узлы идут в
// tun без вопроса, WG/AWG-вопрос - только настоящим WG/AWG-узлам
const pg = computed(() => protoGate(mode.value, inspectRes.value));
watch(
  () => pg.value.proto,
  (p) => {
    if (p) proto.value = p;
  },
);

// ядро движкового пула (88-док + 95-док): mihomo - второй движок;
// выбор появляется у всех источников с WG/AWG/прокси-узлами, когда
// установлен хоть один движок - WG/AWG можно увести в движок и при
// рабочем нативе (старые Кинетики, слово владельца 30.10)
const engineCore = ref('');
const mihomoAvailable = computed(() => !!status.data?.engines?.mihomo?.available);
const singboxAvailable = computed(() => !!status.data?.engines?.singbox?.available);
const showCoreSelect = computed(
  () =>
    mode.value === 'link' &&
    !!inspectRes.value &&
    !inspectRes.value.error &&
    !inspectRes.value.amnezia &&
    ((inspectRes.value.native ?? 0) > 0 || (inspectRes.value.engine ?? 0) > 0) &&
    (singboxAvailable.value || mihomoAvailable.value),
);
const coreOptions = computed<UiSelectOption[]>(() => {
  const opts: UiSelectOption[] = [];
  if (singboxAvailable.value) opts.push({ value: '', label: 'sing-box' });
  if (mihomoAvailable.value) opts.push({ value: 'mihomo', label: 'mihomo (Clash.Meta)' });
  return opts;
});
watch(coreOptions, (opts) => {
  if (!opts.some((o) => o.value === engineCore.value)) {
    engineCore.value = opts[0]?.value || '';
  }
});

// --- cfg-режим: конфиги сразу в диалоге (слово владельца 30.10) ---
// файлы разбираем на клиенте (снифф AWG 3.x по полям конфига), сервер
// остаётся последней инстанцией: нативный пул движковый конфиг не
// сохранит, движковый пул создаётся сразу с ядром из установленных
const files = ref<File[]>([]);
const cfgEngine = ref(''); // '' = нативный Wireguard, singbox|mihomo
const anyEngine = computed(() => singboxAvailable.value || mihomoAvailable.value);
const awg3Sniffed = ref(false);
const awg3Files = ref(0);
// AWG 3.x в дикой природе: HPKey/Paddng/Dr/Dt и хвост диапазонов;
// 2.0-поля (jc/jmin/h1) сюда не попадают - натив их поднимает сам
const AWG3_SNIFF_RE = /hpkey|paddng|rekeyafter|rekeytimeout|rejectafter|keepalivetimeout|maxhandshakeattempts|randomtrailers|disablecookies/i;
// Blob.text() есть не во всех средах (jsdom тестов) - FileReader-фолбэк
function fileText(f: File): Promise<string> {
  if (typeof f.text === 'function') return f.text();
  return new Promise((res, rej) => {
    const r = new FileReader();
    r.onload = () => res(String(r.result || ''));
    r.onerror = () => rej(r.error);
    r.readAsText(f);
  });
}
async function sniffFiles(list: File[]): Promise<void> {
  let hits = 0;
  for (const f of list) {
    if (!/\.conf$/i.test(f.name)) continue;
    try {
      const text = await fileText(f);
      if (AWG3_SNIFF_RE.test(text)) hits++;
    } catch {
      // нечитаемый файл - решит сервер при загрузке
    }
  }
  awg3Files.value = hits;
  awg3Sniffed.value = hits > 0;
  if (awg3Sniffed.value && anyEngine.value && !cfgEngine.value) {
    cfgEngine.value = singboxAvailable.value ? 'singbox' : 'mihomo';
  }
}
function setFiles(list: FileList | null): void {
  files.value = Array.from(list || []);
  cfgEngine.value = '';
  void sniffFiles(files.value);
}
const cfgCoreOptions = computed<UiSelectOption[]>(() => {
  const opts: UiSelectOption[] = [{ value: '', label: 'нативный Wireguard (слот)' }];
  if (singboxAvailable.value) opts.push({ value: 'singbox', label: 'движок sing-box' });
  if (mihomoAvailable.value) opts.push({ value: 'mihomo', label: 'движок mihomo (Clash.Meta)' });
  return opts;
});
const awg3Blocked = computed(
  () => mode.value === 'cfg' && awg3Sniffed.value && !anyEngine.value,
);
const router = useRouter();
function openRouter(path: string): void {
  open.value = false;
  void router.push(path);
}

let inspectTimer: ReturnType<typeof setTimeout> | null = null;
let inspectSeq = 0;
watch(source, (src) => {
  if (mode.value !== 'link') return;
  if (inspectTimer) clearTimeout(inspectTimer);
  const seq = ++inspectSeq;
  if (!src.trim()) {
    inspectRes.value = null;
    inspectErr.value = '';
    return;
  }
  inspecting.value = true;
  inspectTimer = setTimeout(async () => {
    try {
      const ins = await a.inspect(src.trim());
      if (seq !== inspectSeq) return;
      inspectRes.value = ins;
      inspectErr.value = ins.error || '';
    } catch (e) {
      if (seq !== inspectSeq) return;
      inspectRes.value = { error: e instanceof Error ? e.message : String(e), native: 0, engine: 0 };
      inspectErr.value = e instanceof Error ? e.message : String(e);
    } finally {
      if (seq === inspectSeq) inspecting.value = false;
    }
  }, 500);
});

const inspectText = computed(() => {
  if (inspecting.value) return 'разбираю ссылку...';
  if (!inspectRes.value && !inspectErr.value) return '';
  if (inspectErr.value && !inspectRes.value) return 'не разобрать: ' + inspectErr.value;
  return inspectLine(inspectRes.value!);
});
const isAmneziaKey = computed(() => !!inspectRes.value?.amnezia);

// --- предложение установить движок прямо в потоке (слово владельца
// 2026-10-17): узлы требуют sing-box, а его на роутере нет ---
const lxOpen = ref(false);
const engineMissing = computed(() => !status.data?.engine?.available);
const engineWanted = computed(() => {
  if (mode.value === 'free') return true;
  if (mode.value !== 'link' || !inspectRes.value || inspectRes.value.error) return false;
  const ins = inspectRes.value;
  if (ins.amnezia) return ins.amnezia.serviceProtocol === 'vless';
  return ins.engine > 0;
});
const offerInstall = computed(() => engineWanted.value && engineMissing.value);
const offerText = computed(() => {
  const n = mode.value === 'link' && inspectRes.value && !inspectRes.value.amnezia ? inspectRes.value.engine : 0;
  return n > 0
    ? `Для ${n} ${n === 1 ? 'узла' : 'узлов'} нужен движок sing-box, на роутере его нет. Можно поставить сейчас (ядро sing-box-lx, ~30-90 МБ, до пары минут) и продолжить - пул создаётся и до установки.`
    : 'Для этого пула нужен движок sing-box, на роутере его нет. Можно поставить сейчас (ядро sing-box-lx, ~30-90 МБ, до пары минут) и продолжить - пул создаётся и до установки.';
});
function onInstalled(): void {
  push('Ядро sing-box-lx установлено - движок поднимается, узлы заработают после применения');
  void refreshStatus();
}

async function loadSlots(): Promise<void> {
  if (platform.value !== 'keenetic') return;
  slotsLoading.value = true;
  slotsErr.value = '';
  try {
    const all = await a.slots();
    const mawg = new Set((status.data?.pools || []).map((p) => p.slot).filter((s): s is string => !!s));
    const { options, firstFree, firstAny } = slotOptions(all, mawg);
    slotList.value = options;
    if (!slot.value || !options.some((o) => o.value === slot.value)) slot.value = firstFree || firstAny;
  } catch (e) {
    slotList.value = [];
    slotsErr.value = 'Не удалось получить список слотов: ' + (e instanceof Error ? e.message : String(e)) + '. Создание пула заблокировано до появления списка.';
  } finally {
    slotsLoading.value = false;
  }
}

async function addSlot(): Promise<void> {
  await withBusy(async () => {
    try {
      const s = await a.createSlot();
      // ndm применяет создание асинхронно, а дамп интерфейсов на роутере
      // кэшируется - добираем список с ретраями, пока слот не появится
      let found = false;
      for (let i = 0; i < 4 && !found; i++) {
        await loadSlots();
        found = !!s && slotList.value.some((o) => o.value === s);
        if (!found && i < 3) await new Promise((r) => setTimeout(r, 600));
      }
      if (s && !found) {
        // слот на роутере создан - показываем сразу, не заставляя жать ещё раз
        slotList.value = [...slotList.value, { value: s, label: s + ' - свободен', busy: false }];
        found = true;
      }
      if (s && found) {
        slot.value = s;
        push('Создан слот ' + s + ' и выбран для нового пула');
      } else if (s) {
        push('Создан слот ' + s);
      }
    } catch (e) {
      push(e instanceof Error ? e.message : String(e), true);
    }
  });
}

async function withBusy(fn: () => Promise<void>): Promise<void> {
  if (busy.value) return;
  busy.value = true;
  try {
    await fn();
  } finally {
    busy.value = false;
  }
}

// --- капча Free (легаси requestFreePool 571): цикл «запрос - капча - повтор»
// через общий хост; «обновить картинку» = перезапрос без решения
async function askFree(fields: { name: string; fallback: string; keeneticSlot?: string }, answer?: { id: string; solution: string }): Promise<SourcePlan | null> {
  try {
    const r = await a.requestFree(fields, answer);
    if (r && r.captchaRequired && r.captcha && r.captcha.image) {
      const solution = await useCaptcha().ask(
        r.captcha,
        async () => {
          const fresh = await a.requestFree(fields);
          if (!fresh.captcha) throw new Error('капча не получена');
          return fresh.captcha;
        },
      );
      if (!solution) return null; // отмена - остаёмся в форме
      return askFree(fields, solution);
    }
    return r;
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
    return null;
  }
}

function finishPlan(p: SourcePlan): void {
  plan.value = p;
  done.value = true;
  if (p.pool) {
    push(`Пул ${p.pool} создан из источника`, !!(p.warnings && p.warnings.length));
  }
}

async function create(): Promise<void> {
  const body: CreatePoolBody & { name: string; fallback: string; keeneticSlot?: string; openwrtProto?: string } = {
    name: name.value.trim(),
    fallback: fallback.value,
  };
  if (awg3Blocked.value) {
    push('Конфиги AmneziaWG 3.x требуют движок: ни sing-box, ни mihomo не установлены - установите ядро в «Система -> Зависимости»', true);
    return;
  }
  const enginePool = mode.value === 'cfg' && cfgEngine.value !== '';
  if (enginePool) {
    body.engineMode = 'singbox';
    body.engineCore = cfgEngine.value === 'mihomo' ? 'mihomo' : '';
  }
  if (isKeenetic.value && mode.value !== 'free' && !enginePool) {
    if (!slot.value) {
      push(slotsLoading.value ? 'Слоты ещё загружаются, секунду' : 'Слоты недоступны', true);
      return;
    }
    // легаси: занятый слот - подтверждение на замену конфигурации
    const picked = slotList.value.find((o) => o.value === slot.value);
    if (picked?.busy && mode.value === 'cfg') {
      const { useConfirm } = await import('../../stores/confirm');
      const ok = await useConfirm().confirm({
        title: 'Занятый слот',
        text: `Слот ${slot.value} уже используется. При активации пула его конфигурация будет заменена, текущие узлы подключения пропадут. Создать пул на этом слоте?`,
        okLabel: 'Создать',
      });
      if (!ok) return;
    }
    body.keeneticSlot = slot.value;
  }
  if (platform.value === 'openwrt' && !enginePool) body.openwrtProto = proto.value;
  if (mode.value === 'cfg') {
    const picked = [...files.value];
    await withBusy(async () => {
      try {
        await a.create(body);
        if (picked.length) {
          const r = await a.upload(body.name, picked);
          const bad = [...(r.errors || [])];
          if (r.added) push(`Пул ${body.name} создан, конфиги загружены: ${r.added}`);
          else push(`Пул ${body.name} создан, но конфиги не загрузились`, true);
          for (const e of bad) push(e, true);
        } else {
          push(enginePool ? `Пул ${body.name} создан на движке (пустой)` : `Пул ${body.name} создан`);
        }
        open.value = false;
      } catch (e) {
        push(e instanceof Error ? e.message : String(e), true);
      }
    });
    return;
  }
  // link / free
  const src = source.value.trim();
  if (mode.value === 'link' && !src) {
    push('Вставьте ссылку, vpn:// или URL подписки', true);
    return;
  }
  if (!body.name) {
    push('Дайте пулу имя', true);
    return;
  }
  await withBusy(async () => {
    try {
      if (mode.value === 'free') {
        const p = await askFree({ name: body.name, fallback: body.fallback, keeneticSlot: body.keeneticSlot });
        if (p) finishPlan(p);
        return;
      }
      const req: Record<string, unknown> = { ...body, source: src };
      if (isAmneziaKey.value) {
        req.exchange = true;
        req.via = amneziaVia.value;
      }
      if (engineCore.value) req.engineCore = engineCore.value;
      const p = await a.createFromSource(req as never);
      finishPlan(p);
    } catch (e) {
      push(e instanceof Error ? e.message : String(e), true);
    }
  });
}

const pv = computed(() => (plan.value ? planView(plan.value) : null));
const viaOptions = computed<UiSelectOption[]>(() => {
  const opts: UiSelectOption[] = [{ value: '', label: 'напрямую (https)' }];
  for (const t of status.data?.engine?.tuns || []) {
    if (!t.disabled && t.probePort) {
      opts.push({ value: `socks5://127.0.0.1:${t.probePort}`, label: `через пул ${t.pool} (socks5 127.0.0.1:${t.probePort})` });
    }
  }
  return opts;
});
const protoOptions: UiSelectOption[] = [
  { value: 'wireguard', label: 'wireguard' },
  { value: 'amneziawg', label: 'amneziawg' },
];
const fallbackOptions: UiSelectOption[] = [
  { value: 'direct', label: 'пустить трафик напрямую' },
  { value: 'hold', label: 'держать последний конфиг' },
];
</script>

<template>
  <UiDialog
    v-model:open="open"
    :title="done ? 'Пул из источника' : 'Новый интерфейс (пул)'"
    :busy="busy"
    width="560px"
  >
    <!-- фаза результата (план создания) -->
    <template v-if="done && pv">
      <div class="plan">
        <div class="planrow">
          {{ pv.summary }}
        </div>
        <UiHint
          v-if="pv.amneziaMeta"
          :text="pv.amneziaMeta"
        />
        <template v-if="plan?.amnezia?.availableCountries && plan.amnezia.availableCountries.length > 1">
          <UiHint
            :text="'Локации в подписке: ' + plan.amnezia.availableCountries.map((c) => c.code + (c.name ? ' (' + c.name + ')' : '')).join(', ') + '. Сменить - «обновить из источника» в настройках пула.'"
          />
        </template>
        <template v-if="plan?.engine && plan.engine.length">
          <UiHint
            :text="pv.engineSavedForLx
              ? 'Эти узлы сохранены в пуле, но текущий профиль движка их не запустит (xhttp-транспорт и новое vless-шифрование есть только в lx-ядре). После установки lx - «обновить из источника» в настройках пула поднимет их.'
              : 'Узлы, для которых нужен движок sing-box (нативный путь невозможен):'"
          />
          <div
            v-for="e in plan.engine"
            :key="e.tag"
            class="engrow"
          >
            • <span :class="{ secret: addrSecretIn(e.tag) }">{{ e.tag }}</span>
            <UiBadge :text="e.type" />
            <UiBadge
              :text="pv.engineSavedForLx ? 'ждёт lx-ядро' : 'нужен движок'"
              variant="warn"
            />
          </div>
          <UiBanner
            v-if="engineMissing"
            kind="warn"
            text="Движок sing-box не установлен - узлы не заработают. Можно поставить сейчас."
          >
            <UiButton
              variant="primary"
              @click="lxOpen = true"
            >
              Установить ядро sing-box-lx
            </UiButton>
          </UiBanner>
        </template>
        <div
          v-if="plan?.warnings && plan.warnings.length"
          class="warn"
        >
          {{ plan.warnings.join('; ') }}
        </div>
        <div
          v-if="plan?.skipped && plan.skipped.length"
          class="warn"
        >
          Пропущено: {{ plan.skipped.join('; ') }}
        </div>
      </div>
    </template>

    <!-- фаза формы -->
    <template v-else>
      <div class="modes">
        <UiButton
          v-for="m in modeOptions"
          :key="m.value"
          :variant="mode === m.value ? 'primary' : 'ghost'"
          @click="setMode(m.value)"
        >
          {{ m.label }}
        </UiButton>
      </div>
      <UiInput
        v-model="name"
        label="Имя пула"
        placeholder="proton"
        tooltip="строчная латиница, цифры и дефисы, начинается с буквы, до 15 символов"
      />
      <template v-if="mode === 'cfg'">
        <div
          class="cfgdrop"
          @dragover.prevent
          @drop.prevent="setFiles($event.dataTransfer?.files || null)"
        >
          <label class="cfgdrop-btn">
            <input
              type="file"
              multiple
              accept=".conf,.zip"
              class="visually-hidden-input"
              @change="setFiles(($event.target as HTMLInputElement).files)"
            >
            перетащите .conf / .zip сюда или выберите файлы
          </label>
          <span
            v-if="files.length"
            class="cfglist"
          >{{ files.map((f) => f.name).join(', ') }}</span>
        </div>
        <UiBanner
          v-if="awg3Blocked"
          kind="warn"
          text="В конфигах есть AmneziaWG 3.x: ни sing-box, ни mihomo не установлены - такой пул работать не будет. Установите движок в «Система -> Зависимости»."
        >
          <UiButton
            variant="primary"
            @click="openRouter('/next/system')"
          >
            Открыть «Система -> Зависимости»
          </UiButton>
        </UiBanner>
        <UiSelect
          v-else-if="anyEngine"
          v-model="cfgEngine"
          :options="cfgCoreOptions"
          label="Ядро пула"
          :hint="awg3Sniffed
            ? 'Обнаружены конфиги AmneziaWG 3.x (' + awg3Files + '): нативный слот их не поднимет - выбранное ядро обязательно'
            : 'пустой пул на движке: конфиги (включая AWG 3.x) можно загружать после создания'"
        />
      </template>
      <div class="hrow">
        <UiSelect
          v-if="isKeenetic && mode !== 'free' && !(mode === 'cfg' && cfgEngine !== '')"
          v-model="slot"
          :options="slotPickOptions"
          label="Слот Keenetic"
          :disabled="slotsLoading"
          :hint="gate.text || undefined"
        >
          <template #action>
            <UiButton
              :busy="busy"
              tooltip="создать следующий слот WireguardN через CLI роутера"
              @click="addSlot"
            >
              + слот
            </UiButton>
          </template>
        </UiSelect>
        <UiHelp :text="G.slot" />
      </div>
      <UiBanner
        v-if="slotsErr"
        kind="err"
        :text="slotsErr"
      />
      <UiSelect
        v-if="platform === 'openwrt' && mode === 'cfg' && cfgEngine === ''"
        v-model="proto"
        :options="protoOptions"
        label="Протокол (OpenWrt)"
      />
      <div class="hrow">
        <UiSelect
          v-model="fallback"
          :options="fallbackOptions"
          label="Если все конфиги недоступны"
        />
        <UiHelp :text="G.fallback" />
      </div>
      <template v-if="mode === 'link' || mode === 'free'">
        <UiTextarea
          v-if="mode === 'link'"
          v-model="source"
          label="Ссылка, URL подписки или несколько ссылок строками"
          :rows="5"
          mono
          placeholder="vpn://... / wireguard://... / amneziawg://...&#10;vless://... / trojan://...&#10;https://example.com/sub"
        />
        <div
          v-if="showInspect"
          class="hrow"
        >
          <UiHint
            :text="inspectText"
          />
          <UiHelp :text="G.source" />
        </div>
        <UiSelect
          v-if="platform === 'openwrt' && pg.show"
          v-model="proto"
          :options="protoOptions"
          label="Протокол (OpenWrt)"
          :hint="mode === 'link' && inspectRes && inspectRes.native > 0 && inspectRes.engine > 0
            ? 'Прокси-узлы пойдут в tun-интерфейс движка - протокол нужен только WG/AWG-конфигам.'
            : undefined"
        />
        <UiSelect
          v-if="showCoreSelect"
          v-model="engineCore"
          :options="coreOptions"
          label="Ядро движка"
          hint="Альтернатива нативному Wireguard: конфиги пойдут через движок (tun, пробы и ротация так же). Полезно на старых прошивках, где wireguard работает плохо."
        />
        <UiBanner
          v-if="offerInstall"
          kind="warn"
          :text="offerText"
        >
          <UiButton
            variant="primary"
            @click="lxOpen = true"
          >
            Установить ядро sing-box-lx
          </UiButton>
        </UiBanner>
        <div
          v-if="isAmneziaKey"
          class="amn"
        >
          <div class="amntitle">
            Это ключ Amnezia Premium/Free API - mawg обменяет его у gateway Амнезии на конфиг
            <b>{{ (inspectRes?.amnezia?.serviceProtocol || 'awg').toUpperCase() }}</b> и создаст пул.<UiHelp :text="G.gateway" />
          </div>
          <UiSelect
            v-model="amneziaVia"
            :options="viaOptions"
            label="Обмен ключа"
          />
        </div>
        <UiHint
          v-if="mode === 'link'"
          text="wireguard://, amneziawg:// и vpn:// превращаются в обычные конфиги пула (нативно, слот). Узлы vless/trojan и т.п. становятся tun-интерфейсом движка sing-box (tun1, tun2...), который виден в MagiTrickle как обычный интерфейс. Если на роутере движка нет - узлы будут показаны списком с пометкой."
        />
        <UiHint
          v-if="mode === 'free'"
          text="Аккаунт, подписка и ключ не нужны. Доступность и регион определяет официальный gateway. Конфиг Free - AmneziaWG 3.x: нативные интерфейсы его не поднимают, пул работает через ядро sing-box-lx (если ядра нет - установите его в «Система -> Зависимости», пул можно создать и до установки). Gateway может потребовать капчу - она появится для ввода. Пул создаётся выключенным."
        />
      </template>
    </template>

    <template #footer>
      <UiButton
        v-if="done"
        variant="primary"
        @click="open = false"
      >
        Закрыть
      </UiButton>
      <template v-else>
        <UiButton @click="open = false">
          Отмена
        </UiButton>
        <UiButton
          variant="primary"
          :busy="busy"
          @click="create"
        >
          Создать
        </UiButton>
      </template>
    </template>
  </UiDialog>
  <LxInstallDialog
    v-model:open="lxOpen"
    @installed="onInstalled"
  />
</template>

<style scoped>
.cfgdrop {
  margin-top: 6px;
  border: 1px dashed var(--bd, #3a4048);
  border-radius: 8px;
  padding: 10px 12px;
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}
.cfgdrop-btn {
  cursor: pointer;
  color: var(--acc, #4da3ff);
}
.visually-hidden-input {
  position: absolute;
  width: 1px;
  height: 1px;
  opacity: 0;
  overflow: hidden;
}
.cfglist {
  color: var(--mut, #8a8f98);
  font-size: 12px;
  word-break: break-all;
}
.modes {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  margin-bottom: 6px;
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
.amn {
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 8px;
  margin-top: 4px;
}
.amntitle {
  font-size: 12px;
  margin-bottom: 4px;
}
.plan .planrow {
  font-size: 13px;
  margin-bottom: 6px;
  line-height: 1.5;
}
.engrow {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 2px 0;
  font-size: 12px;
}
.warn {
  color: var(--warn);
  margin-top: 6px;
  font-size: 11px;
}
</style>
