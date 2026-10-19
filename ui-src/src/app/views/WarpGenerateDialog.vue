<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiBadge, UiBanner, UiButton, UiCheckbox, UiDialog, UiHint, UiInput, UiSelect, UiSwitch, UiTextarea, type UiSelectOption } from '../../components/ui'
import { useWarp, type WarpGenerateBody, type WarpPlan, type WarpResultOne } from '../../stores/warp'
import { usePools, useStatus } from '../../stores/status'
import { addrSecretIn } from '../secrets'
import { isWarpPool } from './poolsLogic'

// Диалог генератора WARP (77-док): N регистраций устройств Cloudflare ->
// пул с готовыми конфигами, каждый конфиг проверяется живьём. Ключи
// генерируются локально на роутере, наружу уходит только pubkey. Фазы
// как в LxInstallDialog: форма -> прогресс (locked) -> результат.
// Атрибуция обязательна (слово владельца): подход и списки сайтов -
// warp-generator, ссылка в форме и в совете.
const open = defineModel<boolean>('open', { default: false });
const emit = defineEmits<{ created: [] }>();

const warp = useWarp();
const { pools } = usePools();
const { refresh: refreshStatus } = useStatus();

const dlg = ref<{
  phase: 'form' | 'progress' | 'result'
  count: string
  mode: 'random' | 'preset' | 'custom'
  preset: string
  custom: string
  sites: string[]
  subnets: string
  dest: 'download' | 'add' | 'new'
  activate: boolean
  plan: WarpPlan | null
} | null>(null);

const modeOptions: UiSelectOption[] = [
  { value: 'random', label: 'Случайно: у каждого конфига свой адрес:порт' },
  { value: 'preset', label: 'Из проверенного списка' },
  { value: 'custom', label: 'Свой эндпоинт' },
];

watch(open, async (v) => {
  if (!v) return;
  if (!warp.state.loaded) void warp.refresh();
  dlg.value = {
    phase: 'form',
    count: '3',
    mode: 'random',
    preset: warp.state.presets?.presets[0] || '',
    custom: '',
    sites: [],
    subnets: '',
    dest: 'download',
    activate: false,
    plan: null,
  };
});

const maxCount = computed(() => warp.state.presets?.limits.count || 16);
const countErr = computed(() => {
  const n = Number(dlg.value?.count || 0);
  return Number.isInteger(n) && n >= 1 && n <= maxCount.value
    ? null
    : `количество конфигов - от 1 до ${maxCount.value}`;
});
const fixedEndpoint = computed(() => {
  if (dlg.value?.mode === 'preset') return dlg.value.preset;
  if (dlg.value?.mode === 'custom') return dlg.value.custom.trim();
  return '';
});
const fixedErr = computed(() => {
  if (dlg.value?.mode === 'random') return null;
  const ep = fixedEndpoint.value;
  if (!ep) return 'укажите эндпоинт';
  return /^[a-z0-9.[\]:]+$/i.test(ep) && /:\d+$/.test(ep) ? null : 'эндпоинт в форме host:порт';
});
const canGo = computed(() => !!dlg.value && !countErr.value && !fixedErr.value && !subnetErr.value);

const siteList = computed(() => warp.state.presets?.sites || []);

// существующий варп-пул (для «добавить в пул») - владелец: генерация не
// должна плодить новые пулы, если варп уже есть
const warpPoolName = computed(() => {
  for (const p of pools.value || []) {
    if (isWarpPool(p)) return p.name;
  }
  return '';
});
const destOptions = computed<UiSelectOption[]>(() => {
  const opts: UiSelectOption[] = [
    { value: 'download', label: 'Только скачать (роутер не трогаем)' },
  ];
  if (warpPoolName.value) {
    opts.push({ value: 'add', label: 'Добавить в пул ' + warpPoolName.value + ' (с проверкой)' });
  }
  opts.push({ value: 'new', label: 'Создать тестовый пул (с проверкой)' });
  return opts;
});
const IPV4_RE = /^(\d{1,3})(\.(\d{1,3})){3}(\/(\d{1,2}))?$/;

// подсеть или голый IP (v4 - строгая проверка октетов/префикса, v6 - по
// форме); финальный гейт - на сервере (NormalizeSubnet)
function subnetOk(tok: string): boolean {
  const m = IPV4_RE.exec(tok);
  if (m) {
    for (const part of tok.split('/')[0].split('.')) {
      if (Number(part) > 255) return false;
    }
    if (m[5] !== undefined && Number(m[5]) > 32) return false;
    return true;
  }
  return /^[0-9a-fA-F:]+\/\d{1,3}$/.test(tok) || /^[0-9a-fA-F:]+:[0-9a-fA-F]*$/.test(tok);
}

function subnetList(): string[] {
  return (dlg.value?.subnets || '')
    .split(/[\s,;]+/)
    .map((d) => d.trim())
    .filter(Boolean);
}

const subnetErr = computed(() => {
  const bad = subnetList().filter((t) => !subnetOk(t));
  return bad.length ? `не подсети (ждём IP или a.b.c.d/nn): ${bad.join(', ')}` : null;
});

async function run(): Promise<void> {
  if (!dlg.value || !canGo.value) return;
  dlg.value.phase = 'progress';
  const body: WarpGenerateBody = {
    count: Number(dlg.value.count) || 0,
    mode: 'random',
    sites: dlg.value.sites,
    subnets: subnetList(),
  };
  if (dlg.value.mode !== 'random') {
    body.mode = 'fixed';
    body.endpoint = fixedEndpoint.value;
  }
  if (dlg.value.dest !== 'download') {
    body.dest = 'pool';
    if (dlg.value.dest === 'add') {
      body.pool = warpPoolName.value;
      body.activate = dlg.value.activate;
    }
  }
  const plan = await warp.generate(body);
  if (!dlg.value) return;
  dlg.value.plan = plan;
  dlg.value.phase = 'result';
  if (plan.pool) {
    emit('created');
  }
  void refreshStatus();
}

function resultTitle(): string {
  const plan = dlg.value?.plan;
  if (plan?.pool) return `Пул ${plan.pool} создан: конфигов ${plan.added || 0}`;
  if (plan?.error) return 'Генерация не удалась';
  return 'Готово';
}
function resultOk(): boolean {
  return !!dlg.value?.plan?.pool;
}
function resLine(res: WarpResultOne): string {
  if (res.removed) return `${res.endpoint} - не прошёл проверку и удалён: ${res.detail || 'проба не прошла'}`;
  if (res.error) return `${res.endpoint} - ошибка: ${res.error}`;
  if (!res.ok) return `${res.endpoint} - не отвечает: ${res.detail || 'проба не прошла'}`;
  return `${res.endpoint}${res.address ? ' - адрес ' + res.address : ''} - работает: ${res.detail || 'проба прошла'}`;
}

// скачивание в режиме download: файлы собираются в браузере из текстов
// ответа (на сервере их нет - пул не создавался)
function fileSlug(endpoint: string): string {
  return ('warp-' + endpoint.replace(/[^a-zA-Z0-9]+/g, '-')).replace(/-+$/, '').toLowerCase();
}
function dlFile(text: string, name: string): void {
  const a = document.createElement('a');
  a.href = URL.createObjectURL(new Blob([text], { type: 'text/plain' }));
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 5000);
}
function dlHref(res: WarpResultOne, variant: string): string {
  const pool = dlg.value?.plan?.pool || '';
  return `/api/v1/pools/${encodeURIComponent(pool)}/configs/${encodeURIComponent(res.file || '')}` +
    (variant ? '?variant=' + variant : '');
}
</script>

<template>
  <UiDialog
    v-model:open="open"
    title="Генератор WARP"
    :busy="dlg?.phase === 'progress'"
    :locked="dlg?.phase === 'progress'"
    width="520px"
  >
    <template v-if="dlg">
      <template v-if="dlg.phase === 'form'">
        <div class="wtext">
          Регистрируем устройства Cloudflare WARP прямо с роутера и собираем пул.
          Ключи генерируются локально - наружу уходит только публичный.
        </div>
        <UiInput
          v-model="dlg.count"
          type="number"
          label="Сколько конфигов"
          :validator="() => countErr"
        />
        <UiSelect
          v-model="dlg.mode"
          :options="modeOptions"
          label="Эндпоинты"
        />
        <UiSelect
          v-if="dlg.mode === 'preset'"
          v-model="dlg.preset"
          :options="(warp.state.presets?.presets || []).map((p) => ({ value: p, label: p }))"
          label="Проверенная пара"
        />
        <UiInput
          v-if="dlg.mode === 'custom'"
          v-model="dlg.custom"
          label="Эндпоинт"
          placeholder="162.159.192.1:2408"
          :validator="() => fixedErr"
        />
        <UiHint
          v-if="dlg.mode === 'random'"
          :text="'Случайные пары из сетей WARP: ' + (warp.state.presets?.v4Blocks || []).join(', ') + ' и порты ' + (warp.state.presets?.ports || []).slice(0, 6).join(', ') + '... Если провайдер придавит один адрес:порт - выживут остальные.'"
        />
        <details
          class="sites"
          open
        >
          <summary>Разрешённые подсети (AllowedIPs) - что пойдёт через туннель в AmneziaWG и подобных клиентах</summary>
          <UiHint
            v-if="dlg.dest !== 'download'"
            text="Для пула на роутере - только полный туннель (0.0.0.0/0): выбор подсетей недоступен. Пресеты нужны для скачиваемых конфигов (AmneziaWG-клиенты) - пул с «одним ютубом» стал бы чёрной дырой для всего остального трафика."
          />
          <div class="sitelist">
            <UiCheckbox
              v-for="s in siteList"
              :key="s.id"
              :model-value="dlg.sites.includes(s.id)"
              :label="`${s.label} (${s.count})`"
              :disabled="dlg.dest !== 'download'"
              @update:model-value="(v: boolean) => { if (v) dlg?.sites.push(s.id); else { const i = dlg?.sites.indexOf(s.id); if (i !== undefined && i >= 0) dlg?.sites.splice(i, 1) } }"
            />
          </div>
          <UiTextarea
            v-model="dlg.subnets"
            label="Свои подсети"
            placeholder="например: 203.0.113.0/24, 198.51.100.7"
            :disabled="dlg.dest !== 'download'"
            :validator="() => subnetErr"
          />
          <UiHint
            v-if="dlg.dest === 'download' && !dlg.sites.length && !dlg.subnets.trim()"
            text="Ничего не выбрано - туннель на весь интернет (0.0.0.0/0)."
          />
        </details>
        <UiSelect
          v-model="dlg.dest"
          :options="destOptions"
          label="Куда"
        />
        <UiSwitch
          v-if="dlg.dest === 'add'"
          :model-value="dlg.activate"
          label="переключиться на новый конфиг после проверки"
          tight
          @update:model-value="(v: boolean) => dlg && (dlg.activate = v)"
        />
        <UiHint
          v-if="dlg.dest !== 'download'"
          text="Непрошедшие живую проверку конфиги удаляются из пула сразу."
        />
        <UiHint text="Подход генерации и списки подсетей - открытый проект warp-generator: там же можно сгенерировать конфиг вручную - https://warp-generation.github.io/" />
      </template>
      <div
        v-else-if="dlg.phase === 'progress'"
        class="progress"
      >
        {{ dlg.dest === 'download'
          ? 'Регистрируем устройства в Cloudflare и собираем конфиги. Роутер не трогаем, до ~10 секунд на конфиг.'
          : 'Регистрируем устройства в Cloudflare и проверяем каждый конфиг живой пробой. До минуты на конфиг, не закрывайте панель.' }}
      </div>
      <template v-else>
        <div
          class="rstitle"
          :class="resultOk() ? 'ok' : 'err'"
        >
          {{ resultTitle() }}
        </div>
        <template v-if="dlg.plan?.error && !dlg.plan?.pool">
          <div class="rsout">
            {{ dlg.plan.error }}
          </div>
          <UiBanner
            v-if="dlg.plan.unreachable"
            kind="warn"
            text="Оба адреса Cloudflare недоступны с вашей сети. Без анти-DPI пробиться обычно нечем - как с DNS."
          >
            <ul class="advice">
              <li>
                Анти-DPI для роутеров:
                <a
                  href="https://github.com/AloofLibra/zator"
                  target="_blank"
                  rel="noopener"
                >zator</a>
                (обвязка оригинального zapret2 от bol-van). Работает не на всех сетях:
                результат зависит от настроек ТСПУ и требует ручной настройки.
              </li>
              <li>
                Можно сгенерировать конфиг на сайте
                <a
                  href="https://warp-generation.github.io/"
                  target="_blank"
                  rel="noopener"
                >warp-generation.github.io</a>
                и вставить его в панель как конфиг AmneziaWG (сайт генерирует сразу с обфускацией).
              </li>
            </ul>
          </UiBanner>
        </template>
        <template v-else-if="dlg.plan?.configs?.length">
          <div
            v-for="(cf, i) in dlg.plan?.configs || []"
            :key="'c' + i"
            class="resrow"
          >
            <UiBadge
              variant="info"
              text="готов"
            />
            <span
              class="restext"
              :class="{ secret: addrSecretIn(cf.address) }"
            >{{ cf.endpoint }}{{ cf.address ? ' - адрес ' + cf.address : '' }} - на роутер не попал</span>
            <span class="resdl">
              <a
                href="#"
                :download="fileSlug(cf.endpoint) + '.conf'"
                @click.prevent="dlFile(cf.wg, fileSlug(cf.endpoint) + '.conf')"
              >WG</a>
              ·
              <a
                href="#"
                :download="fileSlug(cf.endpoint) + '.awg.conf'"
                @click.prevent="dlFile(cf.awg, fileSlug(cf.endpoint) + '.awg.conf')"
              >AWG</a>
            </span>
          </div>
          <UiHint text="Конфиги существуют только здесь: пул не создавался, туннель на роутере не поднимался - ключ в одном месте. Включайте в клиенте (AmneziaWG/WireGuard)." />
          <UiHint text="AWG-вариант - для клиентов AmneziaWG: обфускация уровня 2.0 (Jc/J/S/H + I1/I2)." />
          <UiHint text="Подход и списки подсетей - проект warp-generator: https://warp-generation.github.io/" />
        </template>
        <template v-else>
          <div
            v-for="(res, i) in dlg.plan?.results || []"
            :key="i"
            class="resrow"
          >
            <UiBadge
              :variant="res.removed || res.error ? 'err' : res.ok ? 'ok' : 'warn'"
              :text="res.removed ? 'удалён' : res.error ? 'ошибка' : res.ok ? 'работает' : 'не отвечает'"
            />
            <span
              class="restext"
              :class="{ secret: addrSecretIn(res.address) }"
            >{{ resLine(res) }}</span>
            <span
              v-if="res.file"
              class="resdl"
            >
              <a
                :href="dlHref(res, '')"
                download
              >WG</a>
              ·
              <a
                :href="dlHref(res, 'awg')"
                download
                title="с полями обфускации для клиентов AmneziaWG (Windows и др.)"
              >AWG</a>
            </span>
          </div>
          <div
            v-for="(w, i) in dlg.plan?.warnings || []"
            :key="'w' + i"
            class="warnline"
          >
            {{ w }}
          </div>
          <UiHint text="AWG-вариант - для внешних клиентов AmneziaWG: тот же конфиг с полями обфускации уровня 2.0. Нативные интерфейсы Кинетиков его не понимают." />
          <UiHint text="Подход и списки подсетей - проект warp-generator: https://warp-generation.github.io/" />
        </template>
      </template>
    </template>
    <template #footer>
      <template v-if="dlg?.phase === 'form'">
        <UiButton @click="open = false">
          Отмена
        </UiButton>
        <UiButton
          variant="primary"
          :disabled="!canGo"
          @click="run"
        >
          Сгенерировать
        </UiButton>
      </template>
      <UiButton
        v-else-if="dlg?.phase === 'result'"
        variant="primary"
        @click="open = false"
      >
        Закрыть
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.wtext {
  font-size: 13px;
  line-height: 1.5;
  margin-bottom: 4px;
}
.sites {
  margin: 6px 0;
}
.sites summary {
  cursor: pointer;
  font-size: 12.5px;
  color: var(--dim);
}
.sitelist {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
  gap: 2px 10px;
  margin: 6px 0;
}
.progress {
  display: flex;
  align-items: center;
  font-size: 13px;
  line-height: 1.5;
}
.rstitle {
  font-weight: 600;
}
.rstitle.ok {
  color: var(--ok);
}
.rstitle.err {
  color: var(--err);
}
.rsout {
  font: 11.5px/1.5 ui-monospace, monospace;
  color: var(--dim);
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 8px;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 40vh;
  overflow: auto;
  margin: 6px 0;
}
.resrow {
  display: flex;
  align-items: baseline;
  gap: 8px;
  font-size: 12.5px;
  line-height: 1.6;
}
.restext {
  flex: 1;
  min-width: 0;
  word-break: break-word;
}
.resdl a {
  color: var(--accent);
}
.warnline {
  font-size: 12px;
  color: var(--dim);
}
.advice {
  margin: 6px 0 0;
  padding-left: 18px;
  font-size: 12px;
  line-height: 1.55;
}
.advice a {
  color: var(--accent);
}
</style>
