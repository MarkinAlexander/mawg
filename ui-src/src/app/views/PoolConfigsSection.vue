<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { UiBadge, UiBusyOverlay, UiButton, UiHelp, UiHint, UiSwitch } from '../../components/ui'
import { usePoolActions } from '../../stores/pools'
import { useToast } from '../../stores/toast'
import { useSecrets } from '../secrets'
import { occupiedV4, nextFreeV4, providerOf } from './poolsLogic'
import { G } from '../glossary'
import type { ConfigView, PoolView } from '../../api/types'
import PoolReaddressDialog from './PoolReaddressDialog.vue'
import { usePools, useStatus } from '../../stores/status'

// Дровер «Конфиги» карточки пула: порядок = приоритет ротации, drag-n-drop
// загрузка, вкл/выкл/перестановка/удаление. У движковых пулов WG/AWG-конфиги
// едут wireguard-эндпоинтами движка (95-док), у нативных - обычная ротация.
const props = defineProps<{ pool: PoolView }>();
const { push } = useToast();
const a = usePoolActions();
const secrets = useSecrets();
const isEng = computed(() => props.pool.settings.engineMode === 'singbox');

// Перенумерация адресов (120-док): пометить можно, когда у однотипной
// кучи адрес один (Proton все с 10.2.0.2) - ровно её переносит одним
// ходом; куча обычно ВЫКЛЮЧЕНА конфликтом - смотрим все конфиги
const { pools } = usePools();
const { refresh: refreshStatus } = useStatus();
const readdressOpen = ref(false);
const readdressHint = computed(() => {
  if (props.pool.premium?.imported || !(props.pool.configs || []).length) return '';
  const prov = providerOf(props.pool.configs?.find((c) => (c.addresses || []).length)?.addresses);
  if (!prov) return '';
  const same = (props.pool.configs || []).filter((c) => (c.addresses || []).some((x) => !x.includes(':') && x.startsWith(prov.prefix)));
  if (same.length < 2) return '';
  return 'одинаковый адрес у ' + same.length + ' конфигов - первый свободный: ' +
    nextFreeV4(occupiedV4(pools.value), prov.prefix);
});

const busy = ref(''); // «act» - одна busy-кнопка секции за раз
// оверлей «идёт применение» (118): загрузка/активация могут висеть
// секундами - движок или ротатор применяют, надо видеть что живо
const busyText = computed(() =>
  busy.value.endsWith(':upload') ? 'загрузка конфигов…' : 'идёт применение настроек…');
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

const cfgOpen = ref(false); // легаси <details> - открытое состояние держится
const fileInput = ref<HTMLInputElement | null>(null);
const dragOver = ref(false);

function cfgRowClass(c: ConfigView): string {
  return c.file === props.pool.activeFile ? 'active' : (c.enabled ? '' : 'dimmed');
}
function pickFiles(): void {
  fileInput.value?.click();
}
function onDrop(e: DragEvent): void {
  dragOver.value = false;
  void doUpload(e.dataTransfer?.files);
}
function onFiles(e: Event): void {
  const inp = e.target as HTMLInputElement;
  void doUpload(inp.files).finally(() => { inp.value = ''; });
}
async function doUpload(files: FileList | null | undefined): Promise<void> {
  if (!files || !files.length) return;
  await run(`${props.pool.name}:upload`, async () => {
    const r = await a.upload(props.pool.name, files);
    let msg = `Добавлено ${r.added}. Дубликаты: ${(r.duplicates || []).length}. Ошибки: ${(r.errors || []).length}`;
    if (r.warnings?.length) msg += '. Конфликты: ' + r.warnings.length;
    push(msg);
    for (const w of r.warnings || []) push(w, true);
    // смена адресов сразу на этапе загрузки (слово владельца 20.11):
    // куча однотипных пришла с одним адресом или с конфликтами - диалог
    // с планом перенумерации открывается сам
    await refreshStatus();
    await nextTick();
    const seen = new Map<string, number>();
    let dup = false;
    for (const c of props.pool.configs || []) {
      const v4 = (c.addresses || []).find((x) => !x.includes(':'))?.split('/')[0];
      if (!v4) continue;
      const n = (seen.get(v4) || 0) + 1;
      if (n >= 2) dup = true;
      seen.set(v4, n);
    }
    if (dup || (r.warnings || []).length) readdressOpen.value = true;
  });
}
function cfgAct(key: string, fn: () => Promise<void>): void {
  void run(key, fn);
}
const coolMin = (sec?: number) => Math.ceil((sec || 0) / 60);
</script>

<template>
  <details
    class="cfg"
    :open="cfgOpen"
    @toggle="cfgOpen = ($event.target as HTMLDetailsElement).open"
  >
    <summary
      :title="isEng
        ? 'конфиги WG/AWG едут wireguard-эндпоинтами движка; порядок = приоритет, включённость учитывается'
        : 'порядок конфигов = приоритет ротации'"
    >
      Конфиги ({{ (pool.configs || []).length }})<UiHelp :text="G.rotation" />
    </summary>
    <UiHint text="порядок списка = приоритет ротации, выше = раньше" />
    <UiBusyOverlay
      :show="!!busy"
      :text="busyText"
    />
    <!-- зона добавления - НАД списком: с десятками конфигов её
         не надо искать в самом низу (слово владельца 30.10) -->
    <div
      v-if="!pool.premium?.imported"
      class="drop"
      :class="{ hover: dragOver }"
      @click="pickFiles"
      @dragover.prevent="dragOver = true"
      @dragleave="dragOver = false"
      @drop.prevent="onDrop"
    >
      перетащите .conf / .zip или нажмите
    </div>
    <input
      ref="fileInput"
      type="file"
      multiple
      accept=".conf,.zip"
      style="display: none"
      @change="onFiles"
    >
    <!-- перенумерация мультипула (120-док): у однотипной кучи адрес один,
         два пула с ним одновременно не поднимутся -->
    <div
      v-if="readdressHint"
      class="readdr"
    >
      <span class="readdr-text">{{ readdressHint }}</span>
      <UiButton
        variant="ghost"
        :disabled="!!busy"
        @click="readdressOpen = true"
      >
        адреса
      </UiButton>
    </div>
    <PoolReaddressDialog
      :pool="pool"
      :open="readdressOpen"
      @update:open="readdressOpen = $event"
    />
    <div class="cfglist">
      <div
        v-for="(c, i) in pool.configs || []"
        :key="c.file"
        class="cfgrow"
        :class="cfgRowClass(c)"
      >
        <span class="cfgmain">
          <span class="cfgname">{{ c.original }}</span>
          <UiBadge
            v-if="c.file === pool.activeFile"
            text="активен"
            variant="ok"
          />
          <UiBadge
            v-if="(c.coolForSec || 0) > 0"
            :text="`cooldown ${coolMin(c.coolForSec)}м`"
            variant="muted"
            :tooltip="G.cooldown"
          />
          <UiBadge
            v-if="c.conflict"
            text="!"
            variant="warn"
            :tooltip="`${c.conflict}: исключён из ротации, переключатель его не включит`"
          />
          <span
            class="cfgend"
            :class="{ secret: secrets.endpoint(c.endpoint) }"
          >{{ c.endpoint }}</span>
        </span>
        <span class="cfgctl">
          <!-- явный выбор активного конфига (слово владельца): бэкенд-эндпоинт
               /activate был, UI не выставлял; конфликт адресов валидирует сервер -->
          <!-- скачивание для внешнего клиента - по уровню конфига (слово
               владельца, третья правка): голый WG - только он сам
               (ничего не накидываем; AWG-вариант - возможно в будущем,
               другим цветом с пометкой «наша обфускация»); 2.0 - обе:
               WG = антиобфускация (снять поля, комментарии остаются),
               AWG = как есть со своими полями; 3.x - никаких -->
          <a
            v-if="c.awg === 'plain'"
            class="cfgdl"
            :href="`/api/v1/pools/${encodeURIComponent(pool.name)}/configs/${encodeURIComponent(c.file)}`"
            :download="c.file"
            title="голый WireGuard - скачивается как есть"
          >WG</a>
          <template v-if="c.awg === 'awg2'">
            <a
              class="cfgdl"
              :href="`/api/v1/pools/${encodeURIComponent(pool.name)}/configs/${encodeURIComponent(c.file)}?variant=plain`"
              :download="c.file"
              title="антиобфускация: снять поля AmneziaWG (голый WireGuard, комментарии остаются)"
            >WG</a>
            <a
              class="cfgdl"
              :href="`/api/v1/pools/${encodeURIComponent(pool.name)}/configs/${encodeURIComponent(c.file)}`"
              :download="c.file"
              title="скачать как есть (поля обфускации уже в конфиге)"
            >AWG</a>
          </template>
          <UiButton
            variant="icon"
            :tooltip="isEng
              ? 'подкинуть движку этот конфиг: пересборка с ним (один активный, как ротация нативных)'
              : 'сделать активным: переключить интерфейс на этот конфиг'"
            :disabled="c.file === pool.activeFile"
            :busy="busy === `${c.file}:activate`"
            @click="cfgAct(`${c.file}:activate`, async () => {
              await a.activateCfg(pool.name, c.file);
              push(`Конфиг активирован: ${c.original}`);
            })"
          >
            ⚡
          </UiButton>
          <UiButton
            variant="icon"
            tooltip="выше в ротации"
            :disabled="i === 0"
            :busy="busy === `${c.file}:up`"
            @click="cfgAct(`${c.file}:up`, () => a.moveCfg(pool.name, c.file, -1))"
          >
            ↑
          </UiButton>
          <UiButton
            variant="icon"
            tooltip="ниже в ротации"
            :disabled="i === (pool.configs || []).length - 1"
            :busy="busy === `${c.file}:down`"
            @click="cfgAct(`${c.file}:down`, () => a.moveCfg(pool.name, c.file, 1))"
          >
            ↓
          </UiButton>
          <UiSwitch
            :model-value="c.enabled"
            tooltip="участвует в ротации"
            :disabled="!!busy"
            @update:model-value="(v: boolean) => cfgAct(`${c.file}:toggle`, () => a.toggleCfg(pool.name, c.file, v))"
          />
          <UiButton
            variant="icon"
            tooltip="удалить конфиг"
            :busy="busy === `${c.file}:del`"
            @click="cfgAct(`${c.file}:del`, () => a.delCfg(pool.name, c.file))"
          >
            ✕
          </UiButton>
        </span>
      </div>
    </div>
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
.cfgend {
  color: var(--dim);
  font-size: 11px;
}
.cfgctl {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex: none;
}
.drop {
  margin-top: 8px;
  border: 1px dashed var(--border);
  border-radius: 8px;
  padding: 14px;
  text-align: center;
  font-size: 12px;
  color: var(--dim);
  cursor: pointer;
}
.drop.hover {
  border-color: var(--accent);
  color: var(--accent);
}
.readdr {
  margin-top: 8px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  border: 1px solid var(--warn);
  border-radius: 8px;
  padding: 6px 10px;
  font-size: 12px;
  color: var(--warn);
}
.readdr-text {
  min-width: 0;
}
.cfgdl {
  color: var(--accent);
  font: 600 10.5px/1 system-ui, sans-serif;
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 3px 5px;
  text-decoration: none;
  white-space: nowrap;
}
</style>
