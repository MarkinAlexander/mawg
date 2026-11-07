<script setup lang="ts">
// Карточка одной группы (тело v-for списка GroupsList): биндинги - из
// контекста useGroupsList (provide/inject), имена прежние - из GroupsView.
import { inject } from 'vue'
import { UiBadge, UiButton, UiIcon, UiInput, UiSelect, UiSwitch } from '../../components/ui'
import { useGroups } from '../../stores/groups'
import { G } from '../glossary'
import { useSecrets } from '../secrets'
import type { GroupView } from '../../api/types'
import {
  degradedText, disabledRulesCount, enabledRulesCount, groupHasDup,
  groupMatchCount, hlSegs, isDeviceGroup, POLICY_SHORT, RULE_HINTS,
  RULE_PLACEHOLDERS, RULE_TYPES, ruleTypeOptions, srcBadge, warnTitle,
} from './groupsLogic'
import { GROUPS_LIST } from './groupsRules'

defineProps<{ g: GroupView }>()

// приватный режим: в правилах скрываются только адреса из конфигов
// (hosts каскадов), домены и чужие подсети видны (слово владельца)
const secrets = useSecrets();
const groups = useGroups();
const { state } = groups;
const {
  openMt, searchQ, armed, dragCtx, dropMark, rowKey, isDragRow,
  onDragStart, onDragOver, onDragLeave, onDrop, onDragEnd,
  devPoolOf, goPool, run, ruleRows, onDetToggle,
  er, editRule, saveRule, cancelRule,
  nrType, nrValue, nrBusy, nrTypeOf, addRule,
  openImport, copyRules, delGroup, bulkBusy, bulkRules, hideOffRulesFlag, editGroup,
} = inject(GROUPS_LIST)!;
</script>

<template>
  <div
    :data-gid="g.id"
    class="mtgroup"
    :class="{ block: g.interface === 'blackhole', dragging: dragCtx?.scope === 'groups' && dragCtx?.id === g.id, 'drop-before': dropMark?.id === g.id && dropMark?.before === true, 'drop-after': dropMark?.id === g.id && dropMark?.before === false }"
    :style="{ '--gcol': g.color || '#4a9eff' }"
    :draggable="isDragRow('groups', g.id)"
    @dragstart="onDragStart($event, 'groups', g.id)"
    @dragover="onDragOver($event, 'groups', g.id)"
    @dragleave="onDragLeave(g.id)"
    @drop="onDrop($event, 'groups', g.id)"
    @dragend="onDragEnd"
  >
    <div class="grow">
      <span
        class="grip"
        title="перетащите, чтобы изменить порядок"
        @pointerdown="armed = rowKey('groups', g.id)"
        @pointerup="armed = ''"
      ><UiIcon name="grip" /></span>
      <span
        class="gcolor"
        :style="{ background: g.color || '#4a9eff' }"
      />
      <b class="gname"><template
        v-for="(s, i) in hlSegs(g.name, searchQ)"
        :key="i"
      ><mark v-if="s.hit">{{ s.text }}</mark><template v-else>{{ s.text }}</template></template></b>
      <span
        v-if="groupHasDup(g, state.duplicates)"
        class="warnb"
        title="в группе есть правила, дублирующиеся в других группах"
      ><UiIcon name="warn" /></span>
      <UiBadge
        v-if="g.cascade"
        text="каскад"
        variant="muted"
        :tooltip="G.cascade"
      />
      <button
        v-if="isDeviceGroup(g) && devPoolOf(g)"
        type="button"
        class="devlink"
        :title="'группа устройства: ВЕСЬ трафик перечисленных IP уходит в пул ' + devPoolOf(g) + '. Клик - к карточке пула'"
        @click="goPool(devPoolOf(g))"
      >
        <UiBadge
          text="устройство целиком"
          variant="ok"
        />
      </button>
      <UiBadge
        v-else-if="isDeviceGroup(g)"
        text="устройство целиком"
        variant="ok"
        tooltip="группа устройства: ВЕСЬ трафик перечисленных IP уходит в этот пул. Стоит последней в списке - при совпадении назначений с другими группами побеждает она. Доступ к роутеру сохраняется всегда"
      />
      <UiBadge
        v-if="g.dnsZone"
        text="зонный DNS"
        variant="muted"
        tooltip="служебная группа зонного DNS: маршрут к серверам зоны через выбранный туннель. Создаётся и обновляется на вкладке DNS («Зонные DNS»), редактировать вручную бессмысленно - применение зоны перезапишет правила"
      />
      <UiBadge
        v-if="g.dnsTunnel"
        text="DNS-туннель"
        variant="muted"
        tooltip="служебная группа туннелирования DNS: маршрут от роутера к DNS-серверам общего списка через выбранный пул. Создаётся и обновляется на вкладке DNS, редактировать вручную бессмысленно"
      />
      <UiBadge
        v-if="g.interface === 'blackhole'"
        text="блокировка"
        variant="err"
        :tooltip="G.blackhole"
      />
      <UiBadge
        v-if="g.policy && g.policy.onDead"
        :text="`если канал пропал: ${POLICY_SHORT[g.policy.onDead] || g.policy.onDead}`"
        variant="warn"
        :tooltip="G.hysteresis"
      />
      <UiBadge
        v-if="srcBadge(g)"
        :text="srcBadge(g)!.text"
        :tooltip="srcBadge(g)!.tooltip"
        variant="ok"
      />
      <span class="ifaceb">
        <UiBadge :text="g.interface" />
        <span
          v-if="g.interfaceTitle"
          class="iftitle"
        >{{ g.interfaceTitle }}</span>
      </span>
      <!-- слово владельца: формат «всего/включено», без чипа - чипы
           только для важных вещей -->
      <span
        class="muted rcount"
        title="правил всего/включено"
      >правил: {{ (g.rules || []).length }}/{{ enabledRulesCount(g) }}<template v-if="searchQ"> · <span class="okc">совпадений: {{ groupMatchCount(g, searchQ) }}</span></template></span>
      <span class="sp" />
      <!-- импорт/копирование правил группы - иконки в шапке (слово
           владельца 2026-10-14: из ряда правил убрать, svg даст позже) -->
      <UiButton
        variant="icon"
        tooltip="вставить список доменов, подсетей или ссылок"
        @click="openImport(g)"
      >
        <UiIcon name="plus-circle" />
      </UiButton>
      <UiButton
        variant="icon"
        tooltip="скопировать все правила группы списком"
        @click="copyRules(g)"
      >
        <UiIcon name="copy" />
      </UiButton>
      <UiSwitch
        :model-value="g.enable"
        :tooltip="g.enable ? 'выключить группу' : 'включить группу'"
        @update:model-value="(v: boolean) => run(() => groups.toggleGroup(g.id, v))"
      />
      <UiButton
        variant="icon"
        tooltip="изменить группу"
        @click="editGroup(g)"
      >
        <UiIcon name="pencil" />
      </UiButton>
      <UiButton
        variant="icon"
        danger-icon
        tooltip="удалить группу"
        @click="delGroup(g)"
      >
        <UiIcon name="trash" />
      </UiButton>
      <div
        v-if="degradedText(g)"
        class="deg muted"
      >
        {{ degradedText(g) }}
      </div>
    </div>
    <details
      :open="openMt.has(g.id)"
      @toggle="onDetToggle(g.id, $event)"
    >
      <summary>правила</summary>
      <div
        v-if="!g.enable"
        class="muted offnote"
      >
        группа выключена - правила сейчас не работают; тумблеры показывают их собственное состояние
      </div>
      <div
        v-if="hideOffRulesFlag && (g.rules || []).length > 0 && ruleRows(g).length === 0"
        class="muted offnote"
      >
        все правила выключены и скрыты тумблером «Скрывать выключенные правила»
      </div>
      <!-- слово владельца: массовое включение/выключение правил (465
           штук по одному не нащёлкаешь); один PUT группы на бэкенде -->
      <div class="bulkrow">
        <UiButton
          :busy="bulkBusy === g.id + ':on'"
          :disabled="disabledRulesCount(g) === 0 && (g.rules || []).length > 0"
          tooltip="включить все правила группы одним действием"
          @click="bulkRules(g, true)"
        >
          включить все ({{ enabledRulesCount(g) }}/{{ (g.rules || []).length }})
        </UiButton>
        <UiButton
          :busy="bulkBusy === g.id + ':off'"
          :disabled="enabledRulesCount(g) === 0"
          tooltip="выключить все правила группы одним действием"
          @click="bulkRules(g, false)"
        >
          выключить все
        </UiButton>
      </div>
      <div class="nrrow">
        <UiSelect
          :model-value="nrTypeOf(g.id)"
          :options="ruleTypeOptions()"
          bare
          class="nrtype"
          @update:model-value="(v: string) => { nrType[g.id] = v }"
        />
        <UiInput
          :model-value="nrValue[g.id] || ''"
          bare
          class="nrval"
          :placeholder="RULE_PLACEHOLDERS[nrTypeOf(g.id)]"
          @update:model-value="(v: string) => { nrValue[g.id] = v }"
        >
          <template #action>
            <UiButton
              :busy="nrBusy === g.id"
              @click="addRule(g)"
            >
              + правило
            </UiButton>
          </template>
        </UiInput>
      </div>
      <div class="muted nrhint">
        {{ RULE_HINTS[nrTypeOf(g.id)] }}
      </div>
      <table class="rules">
        <thead>
          <tr>
            <th class="gripcol" />
            <th>тип</th>
            <th>значение</th>
            <th />
          </tr>
        </thead>
        <tbody>
          <template
            v-for="rl in ruleRows(g)"
            :key="rl.id"
          >
            <tr
              v-if="er && er.groupId === g.id && er.ruleId === rl.id"
              class="erow"
            >
              <td :colspan="4">
                <div class="erwrap">
                  <UiSelect
                    v-model="er.type"
                    :options="ruleTypeOptions()"
                    bare
                    class="ertype"
                  />
                  <UiInput
                    v-model="er.value"
                    bare
                    class="erval"
                  >
                    <template #action>
                      <UiButton
                        variant="primary"
                        @click="saveRule"
                      >
                        сохранить
                      </UiButton>
                      <UiButton @click="cancelRule">
                        отмена
                      </UiButton>
                    </template>
                  </UiInput>
                  <UiInput
                    v-model="er.name"
                    bare
                    class="ername"
                    placeholder="имя (необяз.)"
                  />
                </div>
                <div class="muted erhiant">
                  {{ RULE_HINTS[er.type] || '' }}
                </div>
              </td>
            </tr>
            <tr
              v-else
              :class="{ dimmed: !rl.enable || !g.enable, dragging: dragCtx?.scope === 'rules:' + g.id && dragCtx?.id === rl.id, 'drop-before': dropMark?.id === rl.id && dropMark?.before === true, 'drop-after': dropMark?.id === rl.id && dropMark?.before === false }"
              :draggable="isDragRow('rules:' + g.id, rl.id)"
              @dragstart="onDragStart($event, 'rules:' + g.id, rl.id)"
              @dragover="onDragOver($event, 'rules:' + g.id, rl.id)"
              @dragleave="onDragLeave(rl.id)"
              @drop="onDrop($event, 'rules:' + g.id, rl.id)"
              @dragend="onDragEnd"
            >
              <td class="gripcol">
                <span
                  class="grip"
                  title="перетащите, чтобы изменить порядок"
                  @pointerdown="armed = rowKey('rules:' + g.id, rl.id)"
                  @pointerup="armed = ''"
                ><UiIcon name="grip" /></span>
              </td>
              <td>
                <UiBadge
                  :text="RULE_TYPES[rl.type] || rl.type"
                  variant="muted"
                />
              </td>
              <!-- secret по значению: адрес/подсеть из конфигов (hosts
                   каскадов); домены и 1.1.1.1 видны (слово владельца) -->
              <td
                class="rval"
                :class="{ secret: secrets.rule(rl.rule) }"
              >
                <template
                  v-for="(s, i) in hlSegs(rl.rule, searchQ)"
                  :key="i"
                >
                  <mark v-if="s.hit">{{ s.text }}</mark><template v-else>
                    {{ s.text }}
                  </template>
                </template><span
                  v-if="rl.enable && (state.duplicates[(rl.rule || '').toLowerCase()] || []).length"
                  class="warnb"
                  :title="warnTitle(state.duplicates[(rl.rule || '').toLowerCase()] || [])"
                ><UiIcon name="warn" /></span><span
                  v-if="rl.name"
                  class="muted"
                > (<template
                  v-for="(s, i) in hlSegs(rl.name, searchQ)"
                  :key="i"
                ><mark v-if="s.hit">{{ s.text }}</mark><template v-else>{{ s.text }}</template></template>)</span>
              </td>
              <td class="ract">
                <UiSwitch
                  :model-value="rl.enable"
                  :tooltip="rl.enable ? 'выключить правило' : 'включить правило'"
                  @update:model-value="(v: boolean) => run(() => groups.toggleRule(g.id, rl.id, v))"
                />
                <UiButton
                  variant="icon"
                  tooltip="изменить правило"
                  @click="editRule(g, rl)"
                >
                  <UiIcon name="pencil" />
                </UiButton>
                <UiButton
                  variant="icon"
                  danger-icon
                  tooltip="удалить правило"
                  @click="run(() => groups.deleteRule(g.id, rl.id))"
                >
                  <UiIcon name="trash" />
                </UiButton>
              </td>
            </tr>
          </template>
          <tr v-if="!ruleRows(g).length">
            <td
              colspan="4"
              class="muted"
            >
              правил нет
            </td>
          </tr>
        </tbody>
      </table>
    </details>
  </div>
</template>

<style scoped>
.mtgroup {
  border: 1px solid var(--border);
  border-left: 3px solid var(--gcol);
  border-radius: 8px;
  padding: 8px 10px;
  margin-bottom: 8px;
  background: var(--bg-panel, transparent);
}
.mtgroup.block {
  border-left-color: var(--err);
}
.grow {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.grip {
  color: var(--dim);
  cursor: grab;
  display: inline-flex;
  padding: 2px;
}
.grip:active {
  cursor: grabbing;
}
.gcolor {
  width: 14px;
  height: 14px;
  border-radius: 3px;
  flex: none;
}
.gname {
  word-break: break-all;
}
.warnb {
  color: var(--warn);
  display: inline-flex;
  cursor: help;
}
/* кликабельный бейдж «устройство целиком» -> карточка пула (97-док):
   кнопка без собственной хромированной отделки, вид бейджа не меняем */
.devlink {
  padding: 0;
  border: 0;
  background: none;
  cursor: pointer;
  line-height: 1;
  font: inherit;
}
.ifaceb {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.iftitle {
  color: var(--dim);
  font-size: 11px;
}
.rcount {
  font-size: 12px;
}
.okc {
  color: var(--ok);
}
.sp {
  flex: 1;
}
.deg {
  flex-basis: 100%;
  font-size: 11px;
}
details {
  margin-top: 6px;
}
summary {
  cursor: pointer;
  color: var(--dim);
  font-size: 12px;
  user-select: none;
}
.offnote {
  font-size: 11px;
  margin: 2px 0 6px;
}
.bulkrow {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  justify-content: flex-end;
  margin: 6px 0 2px;
}
.nrrow {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-top: 6px;
}
/* bare-контролы кита несут margin-top: 3px под лейбл - в инлайн-рядах он
   не нужен, вся линия по центру */
.nrrow :deep(.inline),
.erwrap :deep(.inline) {
  margin-top: 0;
}
.nrtype {
  width: 190px;
}
.nrval {
  flex: 1;
  min-width: 180px;
}
.nrhint {
  font-size: 11px;
  margin-top: 2px;
}
.rules {
  width: 100%;
  border-collapse: collapse;
  font-size: 12.5px;
  margin-top: 8px;
}
.rules th {
  text-align: left;
  color: var(--dim);
  font-weight: normal;
  padding: 4px 6px;
  border-bottom: 1px solid var(--border);
}
.rules td {
  padding: 4px 6px;
  border-bottom: 1px solid var(--border);
  word-break: break-all;
}
.gripcol {
  width: 18px;
}
.rval mark,
.gname mark {
  background: rgba(217, 160, 50, 0.35);
  color: inherit;
  padding: 0 2px;
  border-radius: 3px;
}
.ract {
  white-space: nowrap;
  display: flex;
  align-items: center;
  gap: 8px;
  justify-content: flex-end;
}
tr.dimmed td,
.dimmed {
  opacity: 0.45;
}
tr.dragging {
  opacity: 0.4;
}
.drop-before {
  box-shadow: inset 0 2px 0 var(--accent);
}
tr.drop-before td {
  border-top: 2px solid var(--accent);
}
tr.drop-after td {
  border-bottom: 2px solid var(--accent);
}
.mtgroup.drop-before {
  border-top: 2px solid var(--accent);
}
.mtgroup.drop-after {
  border-bottom: 2px solid var(--accent);
}
.erwrap {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  align-items: center;
}
.ertype {
  width: 170px;
}
.erval {
  flex: 1;
  min-width: 180px;
}
.ername {
  width: 160px;
}
.erhiant {
  font-size: 11px;
  margin-top: 2px;
}
@media (max-width: 820px) {
  /* слово владельца: таблица правил на телефоне ломалась в «змейку».
     DOM прежний (DnD/inline-редактор/editingLock не тронуты) - меняется
     только раскладка: строка-карточка, значение на всю ширину */
  .rules {
    display: block;
    border-collapse: separate;
    border-spacing: 0;
  }
  .rules thead {
    display: none;
  }
  .rules tbody {
    display: block;
  }
  .rules tr {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 2px 6px;
    background: var(--panel2);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 6px;
    margin: 0 0 6px;
  }
  .rules td {
    display: inline-flex;
    align-items: center;
    padding: 0;
    border-bottom: none;
  }
  .rules td.gripcol {
    width: auto;
    order: 1;
  }
  .rules td:nth-child(2) {
    order: 2;
  }
  .rules td.ract {
    order: 3;
    margin-left: auto;
  }
  .rules td.rval {
    order: 4;
    flex: 1 1 100%;
  }
  .rules tr.erow {
    display: block;
  }
  .rules tr.erow > td {
    display: block;
  }
  .erwrap {
    flex-direction: column;
    align-items: stretch;
  }
  .ertype,
  .ername {
    width: 100%;
  }
  /* границы drop-индикации - на саму карточку (td-границы на flex не видны) */
  tr.drop-before td,
  tr.drop-after td {
    border: none;
  }
  tr.drop-before {
    box-shadow: inset 0 2px 0 var(--accent);
  }
  tr.drop-after {
    box-shadow: inset 0 -2px 0 var(--accent);
  }
  /* «правил нет» - блоком */
  .rules tr td[colspan] {
    display: block;
  }
}
.muted {
  color: var(--dim);
}
</style>
