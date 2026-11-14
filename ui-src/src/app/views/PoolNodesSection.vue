<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiBadge, UiBusyOverlay, UiButton, UiHelp, UiHint, UiInput, UiSelect, UiSwitch, type UiSelectOption } from '../../components/ui'
import { usePoolActions } from '../../stores/pools'
import { useToast } from '../../stores/toast'
import { G } from '../glossary'
import { groupModeDrawerOptions } from './poolSettingsLogic'
import type { PoolEngine, PoolView } from '../../api/types'

// Дровер «Узлы» движкового пула (60-док): список + пинги из clash_api
// движка, ленивая загрузка по открытию. Режим группы и порядок узлов -
// ПРЯМО здесь (118 TASK 0, слово владельца 14.11: «задавать какой узел
// будет работать, менять их местами - а пока я такого не вижу»): раньше
// селект сидел в шестерёнке, а стрелки рисовались только не в urltest -
// до смены режима управление было невидимо вовсе, дефолт же urltest.
const props = defineProps<{ pool: PoolView }>();
const { push } = useToast();
const a = usePoolActions();

const busy = ref(''); // «pool:act» - одна busy-кнопка секции за раз
// оверлей «идёт применение» (118, слово владельца 14.11: смена режима
// висела на несколько секунд без всякого фидбека, пока движок пересобирается)
const busyText = computed(() => {
  const k = busy.value.slice(props.pool.name.length + 1);
  if (k === 'retest') return 'перемер задержек узлов…';
  if (k === 'nodeoff') return 'пересобираю пул…';
  if (k === 'nodeorder') return 'применяю порядок узлов…';
  if (k === 'selnode') return 'выбираю узел…';
  return 'идёт применение настроек…';
});
async function run(key: string, fn: () => Promise<void>): Promise<void> {
  if (busy.value) return;
  busy.value = key;
  try {
    await fn();
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  busy.value = '';
}

const open = ref(false);
const engineState = ref<PoolEngine | null>(null);
const engineErr = ref('');
async function loadEngine(): Promise<void> {
  engineErr.value = '';
  try {
    engineState.value = await a.engine(props.pool.name);
  } catch (e) {
    engineState.value = null;
    engineErr.value = e instanceof Error ? e.message : String(e);
  }
}
watch(open, (v) => {
  if (v) void loadEngine();
});
const engMode = computed(() =>
  engineState.value?.groupMode || props.pool.settings.groupMode || engineState.value?.mode || 'urltest');

// смена режима сразу применяет: PUT /pools/{name} - merge-patch, движковый
// пул пересобирается сам (pools.go engineRebuild -> applyEngine). rotate
// при RotateSec<10 не тикает (manager_linux.go) - в ротацию входим сразу
// с валидным интервалом: текущий из настроек или 300
const modeSel = computed({
  get: () => engMode.value,
  set: (m: string) => void setMode(m),
});
const rotateSecDraft = ref(String(props.pool.settings.rotateSec || 300));
watch(engMode, (m) => {
  if (m === 'rotate') rotateSecDraft.value = String(props.pool.settings.rotateSec || 300);
});
async function setMode(m: string): Promise<void> {
  if (m === engMode.value) return;
  await run(`${props.pool.name}:mode`, async () => {
    await a.update(props.pool.name, {
      groupMode: m,
      ...(m === 'rotate' ? { rotateSec: Math.max(10, +rotateSecDraft.value || 300) } : {}),
    });
    push('Режим группы: ' + (groupModeDrawerOptions.find((o) => o.value === m)?.label || m));
    await loadEngine();
  });
}
async function applyRotateSec(): Promise<void> {
  const sec = Math.max(10, +rotateSecDraft.value || 300);
  rotateSecDraft.value = String(sec);
  await run(`${props.pool.name}:rotatesec`, async () => {
    await a.update(props.pool.name, { rotateSec: sec });
    push(`Ротация каждые ${sec} с`);
    await loadEngine();
  });
}

// подсказка режима - видимая строка, не «?» в тултипе: следствие смены
// режима должно быть понятно до раскрытия селекта
const modeHint = computed(() => {
  switch (engMode.value) {
    case 'selector':
      return 'Ручной выбор: активен узел, выбранный молнией; при его отказе - другой живой, но выбор остаётся за вами.';
    case 'rotate':
      return `Ротация: mawg меняет узел по кругу каждые ${Math.max(10, +rotateSecDraft.value || 300)} с, перемеряя группу и переходя только на рабочие.`;
    case 'fallback':
      return 'По порядку: активен первый живой узел; при его отказе mawg переходит на следующий живой, а на старший возвращается сам, когда тот стабильно ответит 3 замера подряд.';
    default:
      return 'Лучший пинг (авто): движок сам держит самый быстрый узел. Стрелки порядка здесь ни на что не влияют - станут решающими в режимах «по порядку», «ручной выбор» и «ротация».';
  }
});

async function toggleNode(tag: string, on: boolean): Promise<void> {
  if (busy.value) return;
  await run(`${props.pool.name}:nodeoff`, async () => {
    await a.engineToggle(props.pool.name, tag);
    push(on ? 'Узел исключён из пула' : 'Узел возвращён в пул');
    await loadEngine();
  });
}
async function pickNode(tag: string): Promise<void> {
  if (busy.value) return;
  await run(`${props.pool.name}:selnode`, async () => {
    await a.engineSelect(props.pool.name, tag);
    push(`Выбран узел: ${tag}`);
    await loadEngine();
  });
}
// ручной порядок узлов (13.11): знакомые узлы переживают обновление
// подписок, новые - в конец; движок пересобирается, порядок доехал
async function moveNode(idx: number, dir: -1 | 1): Promise<void> {
  const st = engineState.value;
  if (!st || busy.value) return;
  const j = idx + dir;
  if (j < 0 || j >= st.nodes.length) return;
  const arr = [...st.nodes];
  [arr[idx], arr[j]] = [arr[j], arr[idx]];
  st.nodes = arr;
  await run(`${props.pool.name}:nodeorder`, async () => {
    await a.nodeOrder(props.pool.name, arr.filter((n) => !n.off && n.name).map((n) => n.name as string));
    await loadEngine();
  });
}
async function retestNodes(): Promise<void> {
  if (busy.value) return;
  await run(`${props.pool.name}:retest`, async () => {
    push('Перемер задержек узлов запущен…');
    await a.engineRetest(props.pool.name);
    await loadEngine();
  });
}

// опции селекта режима: короткие подписи в строку + hint в выпадающем
// списке (полные описания - modeHint под строкой)
const modeOptions = groupModeDrawerOptions as UiSelectOption[];
</script>

<template>
  <details
    class="cfg"
    :open="open"
    @toggle="open = ($event.target as HTMLDetailsElement).open"
  >
    <summary title="узлы подписки в группе движка; режим группы и порядок узлов - прямо здесь">
      Узлы ({{ engineState ? engineState.nodes.length : '…' }})<UiHelp :text="G.urltest" />
    </summary>
    <UiHint text="Пинг - замер движка до цели пробы, не пинг самого сервера." />
    <UiHint
      v-if="engineErr"
      :text="'ошибка: ' + engineErr"
    />
    <UiBusyOverlay
      :show="!!busy"
      :text="busyText"
    />
    <div class="moderow">
      <UiSelect
        v-model="modeSel"
        :options="modeOptions"
        bare
        tooltip="режим группы: как движок выбирает рабочий узел"
        :disabled="!!busy"
      />
      <template v-if="engMode === 'rotate'">
        <UiInput
          v-model="rotateSecDraft"
          type="number"
          :step="10"
          bare
          tooltip="интервал ротации, секунд (10-86400)"
          :disabled="!!busy"
          class="rotsec"
          @keydown.enter="applyRotateSec"
        >
          <template #action>
            <UiButton
              variant="icon"
              tooltip="применить интервал ротации"
              :busy="busy === `${pool.name}:rotatesec`"
              :disabled="!!busy"
              @click="applyRotateSec"
            >
              ✓
            </UiButton>
          </template>
        </UiInput>
        <span class="rotlbl">сек</span>
      </template>
    </div>
    <UiHint :text="modeHint" />
    <div
      v-if="engineState"
      class="cfglist"
    >
      <div
        v-for="(n, idx) in engineState.nodes"
        :key="n.tag"
        class="cfgrow"
        :class="{ active: !n.off && engineState.now === n.tag, dimmed: n.off }"
      >
        <span class="cfgmain">
          <span class="cfgname">{{ n.tag }}</span>
          <UiBadge
            v-if="n.type"
            :text="n.type + (n.transport ? '/' + n.transport : '')"
            variant="muted"
            tooltip="протокол узла и его транспорт"
          />
          <UiBadge
            v-if="n.off"
            text="исключён"
            variant="warn"
            tooltip="узел не попадает в конфиг движка; вернуть - тумблером справа"
          />
          <UiBadge
            v-if="!n.off && engineState.now === n.tag"
            text="активен"
            variant="ok"
          />
          <UiBadge
            v-if="!n.off"
            :text="n.delay > 0 ? `${n.delay} мс` : '—'"
            :variant="n.delay > 0 ? 'ok' : 'muted'"
            :tooltip="n.delay > 0 ? 'последний замер движка' : 'не измерен или не отвечает'"
          />
        </span>
        <span class="cfgctl">
          <!-- порядок стрелками виден ВСЕГДА (118 TASK 0): в urltest он
               сохраняется про запас (переживает подписки) и станет решающим
               при смене режима; управляют им по порядку/ручной/ротация -->
          <template v-if="!n.off">
            <UiButton
              variant="icon"
              tooltip="поднять в порядке"
              :disabled="!!busy || idx === 0"
              :busy="busy === `${pool.name}:nodeorder`"
              @click="moveNode(idx, -1)"
            >
              ↑
            </UiButton>
            <UiButton
              variant="icon"
              tooltip="опустить в порядке"
              :disabled="!!busy || idx === (engineState?.nodes.length ?? 0) - 1"
              :busy="busy === `${pool.name}:nodeorder`"
              @click="moveNode(idx, 1)"
            >
              ↓
            </UiButton>
          </template>
          <UiButton
            v-if="engMode === 'selector' && !n.off"
            variant="icon"
            tooltip="выбрать этот узел (selector - ручной режим)"
            :disabled="engineState.now === n.tag"
            :busy="busy === `${pool.name}:selnode`"
            @click="pickNode(n.tag)"
          >
            ⚡
          </UiButton>
          <UiSwitch
            :model-value="!n.off"
            tooltip="участвует в пуле (выключенный узел не попадает в конфиг движка)"
            :disabled="!!busy"
            @update:model-value="(v: boolean) => toggleNode(n.tag, !v)"
          />
        </span>
      </div>
    </div>
    <div
      v-if="engineState"
      class="btnrow"
    >
      <UiButton
        :busy="busy === `${pool.name}:retest`"
        @click="retestNodes"
      >
        Перемерить пинги
      </UiButton>
    </div>
    <!-- порядок переживает обновление подписок (13.11): знакомые узлы на
         местах, новые добавляются в конец -->
    <UiHint text="Порядок стрелками сохраняется и переживает обновление подписок: новые узлы добавляются в конец. Он управляет работой в режимах «по порядку», «ручной выбор» и «ротация»." />
  </details>
</template>

<style scoped>
.cfg {
  margin-top: 10px;
  position: relative; /* якорь оверлея «идёт применение» */
}
.cfg summary {
  cursor: pointer;
  font-size: 13px;
  font-weight: 600;
}
.moderow {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
}
.rotsec {
  width: 92px;
}
.rotlbl {
  color: var(--dim);
  font-size: 12px;
}
.btnrow {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  margin: 10px 0 0;
}
.cfglist {
  display: grid;
  gap: 2px;
  margin-top: 6px;
}
.cfgrow {
  display: flex;
  align-items: center;
  gap: 10px;
  justify-content: space-between;
  padding: 3px 0;
  border-bottom: 1px solid var(--border);
  font-size: 13px;
}
.cfgrow:last-child {
  border-bottom: 0;
}
.cfgrow.active {
  color: var(--ok);
}
.cfgrow.dimmed {
  opacity: 0.45;
}
.cfgmain {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  flex-wrap: wrap;
}
.cfgname {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cfgctl {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex: none;
}
</style>
