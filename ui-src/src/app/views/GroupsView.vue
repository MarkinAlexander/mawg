<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, provide, ref, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import {
  UiBadge, UiBanner, UiButton, UiCard, UiColor, UiDialog, UiHelp, UiHint,
  UiInput, UiSelect,
} from '../../components/ui'
import { useGroups } from '../../stores/groups'
import { G } from '../glossary'
import type { GroupView } from '../../api/types'
import { dupsEntries } from './groupsLogic'
import {
  GROUPS_LIST, useCreateGroup, useCyclesForm, useExcludesDraft,
  useGroupsList, usePresetApply,
} from './groupsRules'
import CascadeDialog from './CascadeDialog.vue'
import GroupDialog from './GroupDialog.vue'
import GroupsList from './GroupsList.vue'

// Фаза 3.7 «Группы правил» (легаси #rulescard, renderRules 1591): пресеты
// (легаси 2148-2259) и каскады (2050-2107); бэкапы и импорт/выгрузка - Ф3.7в.
// Состояние и обработчики - в groupsRules.ts, чистые функции - в groupsLogic.ts.

const groups = useGroups();
const { state } = groups;
const route = useRoute();

onMounted(() => {
  groups.start(30000);
  void groups.loadPresets(); // легаси: на старте, без поллинга
  if (!groups.state.bindingsLoaded) void groups.loadBindings(); // реестр устройств (97-док)
});
onBeforeUnmount(() => groups.stop());

const openMt = ref<Set<string>>(new Set()); // открытые details, переживают перерендер

const { newName, newColor, newIface, creating, createIfaceOptions, createG } = useCreateGroup(openMt);
const { presetSel, presetIface, applying, presetOptions, applyPreset } = usePresetApply(createIfaceOptions);

const cascadeOpen = ref(false);

const { excludesDisabled, exclDraft, exclNew, exclSaving, exclDirty, exclAdd, exclRemove, exclSave } = useExcludesDraft();
const { cycFail, cycRestore, cycSaving, cycChanged, saveCycles } = useCyclesForm();

// список групп/правил (поиск/DnD/правки/диалоги) - в groupsRules.ts;
// карточкам списка (GroupsList/GroupCard) раздаётся через provide/inject
const list = useGroupsList(openMt);
const { searchQ } = list;

const router = useRouter();

const editOpen = ref(false);
const editing = ref<GroupView | null>(null);
function editGroup(g: GroupView): void {
  editing.value = g;
  editOpen.value = true;
}
provide(GROUPS_LIST, { ...list, editGroup });

const dupsOpen = ref(false);
const dupsList = computed(() => dupsEntries(state.groups, state.duplicates, searchQ.value));

// deep-link (44-дока §6): /next/groups?g=<id> открывает редактор группы,
// как только список загрузился (или сразу, если стор уже жив); запрос
// тут же чистится, чтобы F5 не переоткрывал редактор
function openFromLink(): void {
  if (!state.loaded) return;
  const gid = route.query.g;
  if (typeof gid === 'string' && gid) {
    const g = state.groups.find((x) => x.id === gid);
    if (g) editGroup(g);
    router.replace({ query: { ...route.query, g: undefined } });
  }
}
watch(() => route.query.g, openFromLink);
onMounted(openFromLink);
</script>

<template>
  <!-- id - якорь для поиска по настройкам (SearchDialog, search.ts) -->
  <UiCard
    id="card-rules"
    title="Правила маршрутизации"
    subtitle="какой трафик идёт через VPN, а какой - напрямую: правила по доменам, подсетям и отдельным IP."
  >
    <template #actions>
      <UiHelp :text="G.group + ' ' + G.ruleTypes" />
      <UiButton
        tooltip="одинаковые правила в разных группах; с поиском - все группы, где встречается искомое"
        @click="dupsOpen = true"
      >
        показать совпадения в группах
      </UiButton>
      <!-- слово владельца (52/53-доки): кнопка ведёт на общую страницу
           резервных копий - там раскрыто: полный бэкап, восстановление, импорт/выгрузка -->
      <UiButton
        tooltip="страница резервных копий: полный бэкап панели, восстановление, импорт и выгрузка файлом"
        @click="router.push('/next/backups')"
      >
        Резервные копии
      </UiButton>
    </template>

    <!-- ряд создания (легаси rgName/rgColor/rgIface/rgCreate) - одна линия,
         контролы без подписей (легаси-placeholder'ы) -->
    <div class="crow">
      <UiInput
        v-model="newName"
        bare
        placeholder="имя группы"
        class="cname"
      />
      <UiColor
        v-model="newColor"
        bare
        tooltip="цвет группы"
        class="ccolor"
      />
      <UiSelect
        v-model="newIface"
        :options="createIfaceOptions"
        bare
        class="ciface"
      />
      <UiButton
        variant="primary"
        :busy="creating"
        @click="createG"
      >
        + группа
      </UiButton>
    </div>

    <!-- каскад - отдельно, с точным описанием (слово владельца: внимательнее) -->
    <div class="cascdesc">
      <div class="ctext">
        <b>Каскад</b> - направить трафик одного подключения через другое: создаётся
        служебная группа (в списке - с бейджем «каскад»). Адреса серверов источника
        подставляются сами и обновляются при ротации. Включается и выключается
        обычным переключателем группы.<UiHelp :text="G.cascade" />
      </div>
      <UiButton
        tooltip="направить трафик одного подключения через другое"
        @click="cascadeOpen = true"
      >
        + каскад
      </UiButton>
    </div>

    <div class="crow psetrow">
      <UiSelect
        v-model="presetSel"
        :options="presetOptions"
        bare
        class="psel"
      />
      <UiSelect
        v-model="presetIface"
        :options="createIfaceOptions"
        bare
      />
      <UiButton
        :busy="applying"
        @click="applyPreset"
      >
        шаблон на интерфейс
      </UiButton>
    </div>
    <div
      v-if="state.presets.sourceUrl"
      class="muted psetsrc"
    >
      Списки: <a
        :href="state.presets.sourceUrl"
        target="_blank"
        rel="noopener"
      >github.com/itdoginfo/allow-domains</a>{{ state.presets.sourceNote ? ' (' + state.presets.sourceNote + ')' : '' }}. Более точные и свежие списки - в репозитории источника.
    </div>

    <!-- исключения из обработки (94-док): глобальный список - на все
         группы, включая добавленные позже, и на новые правила -->
    <UiBanner
      v-if="excludesDisabled"
      kind="warn"
      text="Исключения временно выключены: разбираемся со странным ростом нагрузки на CPU"
    />
    <div class="excls">
      <div class="exclhead">
        <span class="muted">
          Исключения из обработки<UiHelp text="Трафик на эти адреса и с этих адресов не заворачивается ни в один пул - всегда напрямую (свои серверы, вторые роутеры и т.п.). Действует на все группы, включая добавленные позже. Исключения только для одного пула - в его настройках." />
        </span>
        <span
          v-if="groups.state.excludes.length"
          class="muted"
        >{{ groups.state.excludes.length }}</span>
      </div>
      <div
        v-if="exclDraft.length"
        class="excllist"
      >
        <span
          v-for="(e, i) in exclDraft"
          :key="e"
          class="exclitem"
        >
          {{ e }}
          <button
            type="button"
            class="exclrm"
            title="убрать из списка"
            :disabled="excludesDisabled"
            @click="exclRemove(i)"
          >✕</button>
        </span>
      </div>
      <div class="crow">
        <UiInput
          v-model="exclNew"
          bare
          class="cname"
          placeholder="IP или подсеть; можно несколько через запятую/пробел"
          :disabled="excludesDisabled"
          @keydown.enter="exclAdd"
        />
        <UiButton
          :disabled="excludesDisabled"
          @click="exclAdd"
        >
          + добавить
        </UiButton>
        <UiButton
          variant="primary"
          :disabled="excludesDisabled || !exclDirty"
          :busy="exclSaving"
          @click="exclSave"
        >
          сохранить список
        </UiButton>
      </div>
    </div>

    <!-- гистерезис политик (легаси polFailCycles/polRestoreCycles):
         предложение в одну линию, как в легаси -->
    <div class="cycles">
      <span class="muted">Канал пропал: переводить группы на запасной после</span>
      <UiInput
        v-model="cycFail"
        type="number"
        bare
        class="cnum"
        placeholder="2"
        tooltip="сколько циклов проверки (30с) интерфейс должен считаться мёртвым подряд"
      />
      <span class="muted">циклов отказа, возвращать после</span>
      <UiInput
        v-model="cycRestore"
        type="number"
        bare
        class="cnum"
        placeholder="2"
        tooltip="сколько циклов подряд интерфейс должен быть живым перед возвратом"
      />
      <span class="muted">здоровых циклов</span>
      <UiButton
        :disabled="!cycChanged"
        :busy="cycSaving"
        @click="saveCycles"
      >
        сохранить
      </UiButton>
      <UiHelp :text="G.hysteresis" />
    </div>
    <UiHint
      class="cyshint"
      text="Цикл проверки - каждые 30 секунд. Меньше 2 не рекомендуется: одна потерянная проба будет уводить группы на запасной, а флапающий канал - переключать их каждые 30-60 секунд и рвать соединения. 2/2 - разумный компромисс: реакция ~1-1.5 минуты, возврат только после устойчивого восстановления."
    />

    <!-- баннеры движка MT (легаси updateMTBanners 1559) -->
    <UiBanner
      v-if="state.glitchy"
      kind="err"
      text="Обнаружена глючная сборка MagiTrickle: проба панели поймала усечение групп на массовой записи. Поставьте наш форк (кнопка «обновить MT» в Системе) - проба перепроверится автоматически при смене версии MT; если MT уже наш и свежий, флаг снялся бы при рестарте панели или обновлении."
      :link="{ href: 'https://github.com/MarkinAlexander/magitrickle/releases', label: 'релизы нашего форка' }"
      class="mtnote"
    />
    <UiBanner
      v-if="state.wrapper"
      kind="warn"
      text="В mangle OUTPUT видна обёртка юзера (MR_*): она выводит группы в OUTPUT сама, каскадные хуки mawg не ставятся, чтобы не дублировать обработку."
      class="mtnote"
    />
    <UiBanner
      v-if="state.backendFlavor === 'mawg-c'"
      kind="info"
      text="Бэкенд MT — mt-c: роутер-путь (OUTPUT) обслуживает сам mt-c, каскадные записи mawg в чужие iptables-цепочки отключены. Синхронизация групп через sources API работает как обычно."
      class="mtnote"
    />
    <UiBanner
      v-if="!state.sourcesCapable"
      kind="warn"
      text="Подключённый MagiTrickle не поддерживает источники групп: режимы «кто ходит в группу» спрятаны. Поставьте форк 0.8.2-rev3 или новее."
      class="mtnote"
    />

    <!-- тумблеры сокрытия выключенного, поиск, список групп и диалоги
         импорта/копирования правил (80-док, 44-дока) -->
    <GroupsList />

    <UiDialog
      v-model:open="dupsOpen"
      title="Совпадения в группах"
      width="520px"
    >
      <template v-if="dupsList.length">
        <div
          v-for="e in dupsList"
          :key="e.pat"
          class="duprow"
        >
          <div class="duppat">
            <b>{{ e.pat }}</b>
            <span class="muted"> ({{ e.types }})</span>
            <UiBadge
              v-if="e.dup"
              :text="`дубликат: ${e.groups.length} группы`"
              variant="warn"
            />
          </div>
          <div class="muted dupgroups">
            группы: {{ e.groups.join(', ') }}
          </div>
        </div>
      </template>
      <UiHint
        v-else
        :text="searchQ ? `Ничего не найдено по «${searchQ}».` : 'Межгрупповых дубликатов нет.'"
      />
      <template #footer>
        <UiButton
          variant="primary"
          @click="dupsOpen = false"
        >
          Закрыть
        </UiButton>
      </template>
    </UiDialog>

    <GroupDialog
      v-model:open="editOpen"
      :group="editing"
    />

    <CascadeDialog v-model:open="cascadeOpen" />
  </UiCard>
</template>

<style scoped>
.crow {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
/* bare-контролы кита несут margin-top: 3px под лейбл - в инлайн-рядах он
   не нужен, вся линия по центру */
.crow :deep(.inline),
.cycles :deep(.inline) {
  margin-top: 0;
}
.psetrow {
  margin-top: 8px;
}
.psel {
  flex: 1;
  min-width: 240px;
}
.psetsrc {
  font-size: 11px;
  margin-top: 4px;
}
.cname {
  flex: 1;
  min-width: 180px;
}
.ciface {
  width: 220px;
}
.cascdesc {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 8px;
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: 8px;
}
.ctext {
  flex: 1;
  color: var(--dim);
  font-size: 12px;
}
.ctext b {
  color: var(--text);
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
.cyshint {
  margin-top: 4px;
}
.mtnote {
  margin-top: 6px;
}
.excls {
  display: grid;
  gap: 6px;
  margin-top: 10px;
}
.exclhead {
  display: flex;
  gap: 8px;
  align-items: baseline;
}
.excllist {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.exclitem {
  display: inline-flex;
  gap: 6px;
  align-items: center;
  font-size: 12px;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 3px 8px;
}
.exclrm {
  background: none;
  border: 0;
  color: var(--dim);
  cursor: pointer;
  padding: 0 2px;
  font-size: 12px;
}
.exclrm:hover {
  color: var(--err);
}
.duprow {
  margin-bottom: 10px;
}
.duppat {
  word-break: break-all;
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.dupgroups {
  font-size: 12px;
}
.muted {
  color: var(--dim);
}
</style>
