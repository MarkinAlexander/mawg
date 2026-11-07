<script setup lang="ts">
import { UiButton, UiHelp, UiHint, UiInput } from '../../components/ui';
import { G } from '../glossary';
import type { DeviceBinding } from '../../api/types';

// секция «Устройства через пул» (97-док): чипы адресов привязок с ✕ и
// черновик нового IP. Логика привязок (подтверждения, MT) - в диалоге,
// сюда приезжают список/флаги и возвращаются add/remove
defineProps<{ list: DeviceBinding[]; busy: boolean }>();
const emit = defineEmits<{ add: []; remove: [binding: DeviceBinding] }>();
const draft = defineModel<string>('draft', { default: '' });
</script>

<template>
  <div class="sect">
    Устройства через пул<UiHelp :text="G.deviceBinding" />
  </div>
  <div
    v-if="list.length"
    class="excllist"
  >
    <span
      v-for="b in list"
      :key="b.name"
      class="exclitem"
    >
      {{ b.ips.join(', ') }}
      <button
        type="button"
        class="exclrm"
        title="снять привязку: группа удаляется, трафик устройств пойдёт напрямую"
        @click="emit('remove', b)"
      >✕</button>
    </span>
  </div>
  <div class="hrow">
    <UiInput
      v-model="draft"
      bare
      placeholder="IP устройства (несколько через запятую/пробел)"
      @keydown.enter="emit('add')"
    />
    <UiButton
      :busy="busy"
      :disabled="busy"
      @click="emit('add')"
    >
      + привязать
    </UiButton>
  </div>
  <UiHint text="Весь трафик перечисленных адресов уйдёт в этот пул целиком (группа «Устройство: …» в списке групп). Один адрес может быть привязан только к одному пулу; адрес из глобальных исключений привязкой не оживить. Привязки применяются сразу - «Сохранить» для них нажимать не нужно (оно для остальных настроек)." />
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
</style>
