<script setup lang="ts">
import { ref } from 'vue'
import { UiBadge, UiButton, UiHelp } from '../../components/ui'
import { useSystem } from '../../stores/system'
import type { EngineDiagInfo } from '../../api/types'
import { fmtStarted } from './systemLogic'

// Секция «Диагностика движков» карточки «Система»: живость/версии/хвосты
// логов по кнопке (без опроса), плюс ссылка на полный дамп. Данные -
// GET /system/engines, чистые функции - в systemLogic.ts.
// диагностика движков (112-док 3.7, слово владельца 04.11)
const sys = useSystem();
const diagOpen = ref(false);
const diagBusy = ref(false);
const diagEngines = ref<EngineDiagInfo[]>([]);
const diagMt = ref<EngineDiagInfo | null>(null);
async function loadDiag(): Promise<void> {
  diagOpen.value = !diagOpen.value;
  if (!diagOpen.value) return;
  diagBusy.value = true;
  const r = await sys.enginesDiag();
  if (r) {
    diagEngines.value = r.engines || [];
    diagMt.value = r.mt;
  }
  diagBusy.value = false;
}
</script>

<template>
  <div class="sect">
    Диагностика движков<UiHelp text="Живость, версии, причины отказов и хвосты логов sing-box/mihomo/MagiTrickle (112-док 3.7): когда «движок не запускается», причина видна здесь, без SSH." />
  </div>
  <div class="diagrow">
    <UiButton
      :busy="diagBusy"
      @click="loadDiag"
    >
      {{ diagOpen ? 'скрыть' : 'показать состояние' }}
    </UiButton>
    <a
      class="dlbund"
      href="/api/v1/system/diagnostics"
      download="mawg-diagnostics.txt"
    >собрать диагностику (текстовый файл: версии, конфиги без секретов, логи, события)</a>
  </div>
  <div v-if="diagOpen">
    <div
      v-for="d in diagEngines"
      :key="d.id"
      class="diageng"
    >
      <b>{{ d.id === 'singbox' ? 'sing-box' : d.id }}</b>
      <UiBadge
        :text="d.running ? 'работает' : 'не запущен'"
        :variant="d.running ? 'ok' : 'err'"
      />
      <span
        v-if="d.version"
        class="corenote"
      >{{ d.version }}</span>
      <span
        v-if="d.pid"
        class="corenote"
      >pid {{ d.pid }} c {{ fmtStarted(d.startedAt) }}</span>
      <div
        v-if="d.lastError"
        class="coreerr"
      >
        {{ d.lastError }}
      </div>
      <div
        v-if="d.note"
        class="corenote"
      >
        {{ d.note }}
      </div>
      <div
        v-if="d.logTail && d.logTail.length"
        class="diaglog"
      >
        <div class="corenote">
          лог: {{ d.logPath }}
        </div>
        <div
          v-for="(l, i) in d.logTail"
          :key="i"
        >
          {{ l }}
        </div>
      </div>
    </div>
    <div
      v-if="diagMt"
      class="diageng"
    >
      <b>MagiTrickle</b>
      <span
        v-if="diagMt.version"
        class="corenote"
      >{{ diagMt.version }}</span>
      <span
        v-else
        class="corenote"
      >{{ diagMt.note }}</span>
      <div
        v-if="diagMt.logTail && diagMt.logTail.length"
        class="diaglog"
      >
        <div class="corenote">
          лог: {{ diagMt.logPath }}
        </div>
        <div
          v-for="(l, i) in diagMt.logTail"
          :key="i"
        >
          {{ l }}
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.sect {
  font-weight: 600;
  font-size: 13px;
  margin: 16px 0 6px;
}
.corenote {
  color: var(--dim);
  font-size: 12px;
}
.coreerr {
  color: var(--warn, #b8860b);
  font-size: 12px;
  margin-top: 4px;
  word-break: break-word;
}
.diagrow {
  display: flex;
  gap: 12px;
  align-items: center;
  margin: 6px 0;
}
.dlbund {
  color: var(--dim);
  font-size: 12px;
}
.diageng {
  margin: 6px 0 10px;
}
.diageng > b {
  margin-right: 6px;
}
.diaglog {
  font: 11.5px/1.5 ui-monospace, monospace;
  color: var(--dim);
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 6px 8px;
  margin-top: 4px;
  max-height: 160px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
