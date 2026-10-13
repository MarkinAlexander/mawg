<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiButton, UiDialog, UiHint, UiInput, UiSelect, type UiSelectOption } from '../../components/ui'
import { useGroups } from '../../stores/groups'
import { useToast } from '../../stores/toast'
import type { GroupPolicy, GroupView } from '../../api/types'
import { POLICY_HINTS, POLICY_OPTIONS } from './groupsLogic'

// Диалог группы (легаси editGroupDlg 1364 + egSave 2127). Ф3.7а - база
// (имя/цвет/интерфейс, у каскадной залочен) + политика отвала (у
// blackhole скрыта, шлётся onDead:''). mtSetPolicy заменяет политику
// ЦЕЛИКОМ (server.go) - sources-поля пересылаем как есть, их редактор
// появится в Ф3.7б.
const props = defineProps<{ group: GroupView | null }>();
const open = defineModel<boolean>('open', { default: false });

const { state, updateGroup, setPolicy } = useGroups();
const { push } = useToast();

const name = ref('');
const color = ref('#4a9eff');
const iface = ref('');
const onDead = ref<'' | 'direct' | 'iface'>('');
const policyIface = ref('');
const busy = ref(false);

const isCascade = computed(() => !!props.group?.cascade);
const isBlock = computed(() => iface.value === 'blackhole');

// интерфейсы группы (легаси 1370-1376): устройства + blackhole + текущий
const ifaceOptions = computed<UiSelectOption[]>(() => {
  const list = state.ifaces.map((i) => ({
    value: i.device,
    label: i.device + (i.title && i.title !== i.device ? ' - ' + i.title : ''),
  }));
  const out = list.filter((o) => o.value !== 'blackhole');
  out.push({ value: 'blackhole', label: 'blackhole - (блокировать трафик)' });
  const cur = iface.value;
  if (cur && !out.some((o) => o.value === cur)) out.push({ value: cur, label: cur });
  return out;
});

// запасной интерфейс (легаси 1392-1394): без blackhole, текущий если пропал
const policyIfaceOptions = computed<UiSelectOption[]>(() => {
  const list = state.ifaces.filter((i) => i.device !== 'blackhole');
  const out = list.map((i) => ({
    value: i.device,
    label: i.device + (i.title && i.title !== i.device ? ' - ' + i.title : ''),
  }));
  const cur = policyIface.value;
  if (cur && !out.some((o) => o.value === cur)) out.push({ value: cur, label: cur });
  return out;
});

watch(open, (v) => {
  if (!v) return;
  const g = props.group;
  if (!g) return;
  name.value = g.name;
  color.value = /^#[0-9a-fA-F]{6}$/.test(g.color || '') ? g.color! : '#4a9eff';
  iface.value = g.interface;
  const pol: GroupPolicy = g.policy || {};
  onDead.value = pol.onDead || '';
  policyIface.value = pol.iface || '';
});

async function save(): Promise<void> {
  const g = props.group;
  const nm = name.value.trim();
  if (!g || !nm) return;
  busy.value = true;
  try {
    // mtSetPolicy затирает отсутствующие поля - sources пересылаем как есть
    const pol = g.policy || {};
    const sources: GroupPolicy = {
      onDead: isBlock.value ? '' : onDead.value,
      iface: isBlock.value ? '' : policyIface.value,
      sourcesMode: pol.sourcesMode || '',
      sources: pol.sources || [],
      sourcesRouter: !!pol.sourcesRouter,
    };
    await updateGroup(g.id, { name: nm, color: color.value, interface: iface.value });
    await setPolicy(g.id, sources);
    open.value = false;
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  busy.value = false;
}
</script>

<template>
  <UiDialog
    v-model:open="open"
    title="Изменить группу"
    :busy="busy"
    width="440px"
  >
    <UiInput
      v-model="name"
      label="Имя"
    />
    <div class="frow">
      <span class="flbl">Цвет</span>
      <input
        v-model="color"
        type="color"
        class="fcolor"
        title="цвет группы"
      >
    </div>
    <UiSelect
      v-model="iface"
      :options="ifaceOptions"
      label="Интерфейс"
      :disabled="isCascade"
      :tooltip="isCascade ? 'интерфейс каскадной группы задается при создании каскада' : ''"
    />
    <template v-if="!isBlock">
      <UiSelect
        v-model="onDead"
        :options="POLICY_OPTIONS"
        label="Если основной интерфейс недоступен"
        :hint="POLICY_HINTS[onDead] || ''"
      />
      <template v-if="onDead === 'iface'">
        <UiSelect
          v-model="policyIface"
          :options="policyIfaceOptions"
          label="Запасной интерфейс"
        />
      </template>
    </template>
    <UiHint
      v-else
      text="Блокировка (blackhole): трафик группы отбрасывается, политика отвала не применяется."
    />
    <template #footer>
      <UiButton @click="open = false">
        Отмена
      </UiButton>
      <UiButton
        variant="primary"
        :busy="busy"
        @click="save"
      >
        Сохранить
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.frow {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 10px 0 2px;
}
.flbl {
  font-size: 13px;
}
.fcolor {
  width: 42px;
  height: 30px;
  padding: 2px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: transparent;
  cursor: pointer;
}
</style>
