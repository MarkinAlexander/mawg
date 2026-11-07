<script setup lang="ts">
// Список групп: тумблеры сокрытия выключенного, поиск, сам список
// (GroupCard) и диалоги импорта/копирования правил. Биндинги - из
// контекста useGroupsList (provide/inject), имена прежние - из GroupsView.
import { inject } from 'vue'
import {
  UiButton, UiDialog, UiHelp, UiHint, UiInput, UiSelect, UiSwitch, UiTextarea,
} from '../../components/ui'
import { useGroups } from '../../stores/groups'
import { G } from '../glossary'
import { IMPORT_TYPE_OPTIONS } from './groupsLogic'
import { GROUPS_LIST } from './groupsRules'
import GroupCard from './GroupCard.vue'

const { state } = useGroups();
const {
  q, searchQ, visible, offGroupsCount, offRulesCount,
  hideOffGroupsFlag, hideOffRulesFlag, setHideOffGroups, setHideOffRules,
  listBox, onFocusin, onFocusout,
  imOpen, imG, imType, imSecond, imEnable, imText, imBusy, doImport,
  cpOpen, cpText, cpTitle,
} = inject(GROUPS_LIST)!;
</script>

<template>
  <!-- фильтры выключенного (80-док, бонус): два раздельных тумблера
       (слово владельца: группы скрывать, правила видеть) со счётчиками -->
  <div class="offrow">
    <span class="offitem offgroups">
      <UiSwitch
        :model-value="hideOffGroupsFlag"
        label="Скрывать выключенные группы"
        @update:model-value="setHideOffGroups"
      />
      <span class="muted offcount">выключено: {{ offGroupsCount }}</span>
    </span>
    <span class="offitem offrules">
      <UiSwitch
        :model-value="hideOffRulesFlag"
        label="Скрывать выключенные правила"
        @update:model-value="setHideOffRules"
      />
      <span class="muted offcount">выключено: {{ offRulesCount }}</span>
    </span>
  </div>

  <div class="srow">
    <UiInput
      v-model="q"
      label="поиск"
      placeholder="поиск: показываются только группы с совпадениями"
      class="sinput"
    />
  </div>

  <div
    ref="listBox"
    class="mtlist"
    @focusin="onFocusin"
    @focusout="onFocusout"
  >
    <UiHint
      v-if="!state.loaded"
      text="загружаю группы..."
    />
    <UiHint
      v-else-if="!state.available"
      text="MagiTrickle недоступен"
    />
    <UiHint
      v-else-if="hideOffGroupsFlag && !visible.length"
      text="Под фильтр «Скрывать выключенные группы» не попало ни одной включённой группы - выключите тумблер, чтобы увидеть остальные."
    />
    <UiHint
      v-else-if="searchQ && !visible.length"
      :text="`Ничего не найдено по «${searchQ}».`"
    />
    <UiHint
      v-else-if="!state.groups.length"
      text="Групп нет. Создайте группу или примените шаблон."
    />

    <GroupCard
      v-for="g in visible"
      :key="g.id"
      :g="g"
    />
  </div>

  <UiDialog
    v-model:open="imOpen"
    :title="imG ? 'Импорт правил в ' + imG.name : 'Импорт правил'"
    :busy="imBusy"
    width="520px"
  >
    <div class="hrow">
      <UiSelect
        v-model="imType"
        :options="IMPORT_TYPE_OPTIONS"
        label="Тип для доменов"
      />
      <UiHelp :text="G.cidr" />
    </div>
    <UiHint text="Ссылки всегда очищаются до домена: https://www.ozon.ru/path -> www.ozon.ru, ftp://files.example.com/dir -> files.example.com" />
    <UiSwitch
      v-model="imSecond"
      label="Усекать до второго уровня"
      hint="www.ozon.ru -> ozon.ru, зоны вроде bbc.co.uk не трогаются"
    />
    <UiSwitch
      v-model="imEnable"
      label="Импортировать включёнными"
      hint="выключенное правило можно включить позже переключателем"
    />
    <UiTextarea
      v-model="imText"
      label="Список: по одному в строке, пробелы/запятые/точки с запятой тоже разделяют"
      :rows="10"
      mono
    />
    <UiHint text="Дубликаты внутри списка и совпадения с существующими правилами пропускаются. Кириллические домены вставляйте в punycode (латинская запись вида xn--...)." />
    <template #footer>
      <UiButton
        :disabled="imBusy"
        @click="imOpen = false"
      >
        отмена
      </UiButton>
      <UiButton
        variant="primary"
        :busy="imBusy"
        @click="doImport"
      >
        импортировать
      </UiButton>
    </template>
  </UiDialog>

  <!-- фолбэк копирования (легаси dlgCopy) -->
  <UiDialog
    v-model:open="cpOpen"
    :title="cpTitle"
    width="520px"
  >
    <textarea
      :value="cpText"
      readonly
      rows="12"
      class="cptext"
      spellcheck="false"
    />
    <UiHint text="Если не скопировалось само - выделено, нажмите Ctrl+C." />
    <template #footer>
      <UiButton
        variant="primary"
        @click="cpOpen = false"
      >
        Закрыть
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
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
.srow {
  display: flex;
  margin-top: 10px;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.sinput {
  flex: 1;
  min-width: 200px;
}
.offrow {
  display: flex;
  gap: 8px 22px;
  flex-wrap: wrap;
  margin-top: 12px;
}
.offitem {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
.offcount {
  font-size: 12px;
}
.mtlist {
  margin-top: 10px;
}
.cptext {
  width: 100%;
  box-sizing: border-box;
  font: 12px/1.5 ui-monospace, monospace;
  background: var(--bg-input, transparent);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 6px;
}
.muted {
  color: var(--dim);
}
</style>
