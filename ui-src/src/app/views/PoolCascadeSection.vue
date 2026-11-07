<script setup lang="ts">
import type { UiSelectOption } from '../../components/ui';
import { UiHelp, UiHint, UiInput, UiOrderList, UiSelect, UiSwitch } from '../../components/ui';
import { G } from '../glossary';
import { scModeOptions, scTrigOptions } from './poolSettingsLogic';

// секция «Умный каскад» (86-док): Via - упорядоченный список
// (порядок = приоритет). Все поля - модели диалога: предзаполнение и
// сборка тела smartCascade живут там
defineProps<{ poolOptions: UiSelectOption[] }>();
const on = defineModel<boolean>('on', { default: false });
const via = defineModel<string[]>('via', { default: () => [] });
const trigMode = defineModel<string>('trigMode', { default: 'exhaust' });
const trigFails = defineModel<string>('trigFails', { default: '3' });
const mode = defineModel<string>('mode', { default: 'schedule' });
const interval = defineModel<string>('interval', { default: '6' });
const times = defineModel<string>('times', { default: '03:00' });
const dwell = defineModel<string>('dwell', { default: '10' });
</script>

<template>
  <div class="sect">
    Умный каскад
  </div>
  <div class="hrow">
    <UiSwitch
      v-model="on"
      label="спасать пул через запасной при блокировке"
      tooltip="адрес VPN сохраняется: трафик пула идёт внутри туннеля запасного"
    />
    <UiHelp :text="G.smartCascade" />
  </div>
  <template v-if="on">
    <div class="hrow">
      <UiSelect
        v-model="trigMode"
        :options="scTrigOptions"
        label="Когда включать каскад"
      />
    </div>
    <UiInput
      v-if="trigMode === 'fails'"
      v-model="trigFails"
      type="number"
      label="Отказов подряд до каскада"
      tooltip="после N отказов активного конфига mawg сразу пробует ЭТОТ ЖЕ конфиг через запасной пул; не ожил - следующий конфиг напрямую, и так по кругу"
    />
    <UiOrderList
      v-model="via"
      :options="poolOptions"
      label="добавить запасной пул"
      empty-hint="Запасных нет: при блокировке пул не сможет уйти в каскад - добавьте хотя бы один пул."
      order-hint="Первый живой сверху становится запасным путём; порядок списка = приоритет."
    />
    <div class="hrow">
      <UiSelect
        v-model="mode"
        :options="scModeOptions"
        label="Когда пробовать прямой путь обратно"
      />
    </div>
    <UiInput
      v-if="mode === 'interval'"
      v-model="interval"
      type="number"
      label="Интервал проверки, часов"
    />
    <UiInput
      v-if="mode === 'schedule'"
      v-model="times"
      label="Время проверки, ЧЧ:ММ через запятую"
      tooltip="например: 03:00, 05:30 - в эти моменты панель пробует прямой путь и снимает каскад, если он ожил"
    />
    <UiInput
      v-model="dwell"
      type="number"
      label="Антифлап: минут в состоянии до авто-возврата"
      tooltip="0 = 10 минут; кнопка «проверить сейчас» гейт минует"
    />
    <UiHint text="При срабатывании создаётся выключенная служебная группа «Служебная: этот пул через запасной». Пробы возврата идут в коротком окне без её правила: на секунды трафик пробует прямой путь." />
  </template>
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
.sect {
  font-weight: 600;
  font-size: 13px;
  margin: 12px 0 4px;
}
</style>
