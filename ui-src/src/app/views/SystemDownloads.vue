<script setup lang="ts">
import { ref, watch } from 'vue'
import { UiDialog, UiHint } from '../../components/ui'
import { useSystem } from '../../stores/system'
import type { CoreDownloadInfo } from '../../api/types'
import { dlMb } from './systemLogic'

// Диалог «Скачать бинари движков» (112-док 4.1): прямые ссылки на релизные
// ассеты под архитектуру роутера. Открывается из секции «Ядра VPN»
// (SystemCores), список подтягивается при каждом открытии.
const open = defineModel<boolean>('open', { default: false });
const sys = useSystem();

const dlBusy = ref(false);
const dlSingbox = ref<CoreDownloadInfo[]>([]);
const dlSingboxAlt = ref<CoreDownloadInfo[]>([]);
const dlMihomo = ref<CoreDownloadInfo[]>([]);
const dlMt = ref<CoreDownloadInfo[]>([]);
const dlArch = ref('');
async function openDownloads(): Promise<void> {
  dlBusy.value = true;
  const r = await sys.coreDownloads();
  if (r) {
    dlArch.value = r.arch;
    dlSingbox.value = r.singbox || [];
    dlSingboxAlt.value = r.singboxAlt || [];
    dlMihomo.value = r.mihomo || [];
    dlMt.value = r.magitrickle || [];
  }
  dlBusy.value = false;
}
watch(open, (v) => {
  if (v) void openDownloads();
});
</script>

<template>
  <UiDialog
    v-model:open="open"
    title="Скачать бинари движков"
    width="520px"
  >
    <div class="lxtext">
      Файлы под этот роутер{{ dlArch ? ` (архитектура ${dlArch})` : '' }} - если с роутера
      GitHub недоступен, скачайте где есть доступ (точные имена файлов - под ссылками,
      их же можно найти на GitHub вручную), затем залейте кнопкой «Загрузить».
      Бинари sing-box/mihomo - распаковать на компьютере (tar.gz / gz), пакет MT - как есть.
    </div>
    <div v-if="dlBusy">
      ссылки уточняются (GitHub)...
    </div>
    <template v-else>
      <div class="dlhead">
        sing-box
      </div>
      <div
        v-for="d in dlSingbox"
        :key="d.label"
        class="dllink"
      >
        <b>{{ d.label }}</b>
        <a
          v-if="d.plain"
          :href="d.plain.url"
          target="_blank"
          rel="noopener"
        >скачать ({{ dlMb(d.plain.size) }})</a>
        <a
          v-if="d.compact"
          :href="d.compact.url"
          target="_blank"
          rel="noopener"
        >сжатый upx ({{ dlMb(d.compact.size) }})</a>
        <span class="corenote mono">{{ d.plain?.asset }}</span>
      </div>
      <details
        v-if="dlSingboxAlt.length"
        class="dlalt"
      >
        <summary>другие сборки sing-box</summary>
        <div
          v-for="d in dlSingboxAlt"
          :key="d.label"
          class="dllink"
        >
          <span>{{ d.label }}</span>
          <a
            v-if="d.plain"
            :href="d.plain.url"
            target="_blank"
            rel="noopener"
          >скачать ({{ dlMb(d.plain.size) }})</a>
          <a
            v-if="d.compact"
            :href="d.compact.url"
            target="_blank"
            rel="noopener"
          >сжатый ({{ dlMb(d.compact.size) }})</a>
          <span class="corenote mono">{{ d.plain?.asset }}</span>
        </div>
      </details>
      <div class="dlhead">
        mihomo
      </div>
      <div
        v-for="d in dlMihomo"
        :key="d.label"
        class="dllink"
      >
        <b>{{ d.label }}</b>
        <a
          v-if="d.plain"
          :href="d.plain.url"
          target="_blank"
          rel="noopener"
        >скачать ({{ dlMb(d.plain.size) }})</a>
        <span class="corenote mono">{{ d.plain?.asset }}</span>
      </div>
      <div class="dlhead">
        MagiTrickle
      </div>
      <div
        v-for="d in dlMt"
        :key="d.label"
        class="dllink"
      >
        <b>{{ d.label }}</b>
        <a
          v-if="d.plain"
          :href="d.plain.url"
          target="_blank"
          rel="noopener"
        >скачать ({{ dlMb(d.plain.size) }})</a>
        <span class="corenote mono">{{ d.plain?.asset }}</span>
      </div>
      <UiHint
        v-if="!dlMt.length"
        text="пакет MagiTrickle под этот роутер не определился - обновляйте MT пунктом в таблице зависимостей выше"
      />
    </template>
  </UiDialog>
</template>

<style scoped>
.dlhead {
  font-weight: 600;
  font-size: 12.5px;
  margin: 10px 0 2px;
}
.mono {
  font-family: ui-monospace, monospace;
  word-break: break-all;
}
.dllink {
  margin: 6px 0;
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: baseline;
}
.dllink > a {
  font-size: 12px;
}
.dllink > b {
  font-size: 12.5px;
}
.dlalt,
.dlmt {
  margin-top: 10px;
}
.dlalt summary,
.dlmt summary {
  color: var(--dim);
  font-size: 11.5px;
  cursor: pointer;
}
.lxtext {
  color: var(--dim);
  font-size: 12px;
  line-height: 1.5;
  margin-bottom: 10px;
}
.corenote {
  color: var(--dim);
  font-size: 12px;
}
</style>
