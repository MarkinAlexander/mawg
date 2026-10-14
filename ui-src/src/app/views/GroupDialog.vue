<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiButton, UiColor, UiDialog, UiHint, UiInput, UiSelect, type UiSelectOption, UiSwitch } from '../../components/ui'
import { useGroups } from '../../stores/groups'
import { useToast } from '../../stores/toast'
import type { GroupPolicy, GroupView } from '../../api/types'
import {
  parseSources, POLICY_HINTS, POLICY_OPTIONS, srcListInitial, SRC_MODE_HINTS,
  SRC_MODE_OPTIONS, srcModeInitial,
} from './groupsLogic'

// Диалог группы (легаси editGroupDlg 1364 + egSave 2127): база (имя/цвет/
// интерфейс, у каскадной залочен), источники (режимы all/all+router/list -
// Ф3.7б; у каскадов задаются при создании, старый MT без sources - блок
// скрыт и режим принудительно all) и политика отвала (у blackhole скрыта,
// шлётся onDead:''). mtSetPolicy заменяет политику ЦЕЛИКОМ (server.go) -
// шлются все пять полей.
const props = defineProps<{ group: GroupView | null }>();
const open = defineModel<boolean>('open', { default: false });

const { state, updateGroup, setPolicy } = useGroups();
const { push } = useToast();

const name = ref('');
const color = ref('#4a9eff');
const iface = ref('');
const onDead = ref<'' | 'direct' | 'iface'>('');
const policyIface = ref('');
const srcMode = ref<'' | 'all' | 'all+router' | 'list'>('all');
const srcList = ref('');
const srcRouter = ref(false);
const busy = ref(false);

const isCascade = computed(() => !!props.group?.cascade);
const isBlock = computed(() => iface.value === 'blackhole');
const srcSupported = computed(() => state.sourcesCapable);
const srcVisible = computed(() => !isCascade.value && srcSupported.value);
const srcListVisible = computed(() => srcMode.value === 'list');

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
  // кто ходит в группу (легаси 1379-1389)
  srcMode.value = srcModeInitial(g);
  srcList.value = srcListInitial(g);
  srcRouter.value = !!pol.sourcesRouter;
  if (!srcSupported.value && !isCascade.value) srcMode.value = 'all';
});

async function save(): Promise<void> {
  const g = props.group;
  const nm = name.value.trim();
  if (!g || !nm) return;
  // источники: у каскадов задаются при создании - глушим (легаси 2141-2142);
  // старый MT без sources - режим принудительно all (легаси 1386-1389)
  const mode: GroupPolicy['sourcesMode'] = isCascade.value ? '' : (srcSupported.value ? srcMode.value : 'all');
  const sources = isCascade.value || !srcSupported.value ? [] : parseSources(srcList.value);
  const router = mode === 'list' && srcRouter.value;
  if (!isCascade.value && srcSupported.value && srcMode.value === 'list' && !sources.length && !srcRouter.value) {
    push('Пустой список: добавьте IP или включите роутер', true);
    return;
  }
  busy.value = true;
  try {
    const policy: GroupPolicy = {
      onDead: isBlock.value ? '' : onDead.value,
      iface: isBlock.value ? '' : policyIface.value,
      sourcesMode: mode,
      sources,
      sourcesRouter: router,
    };
    await updateGroup(g.id, { name: nm, color: color.value, interface: iface.value });
    await setPolicy(g.id, policy);
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
    <UiColor
      v-model="color"
      label="Цвет"
      tooltip="цвет группы"
    />
    <UiSelect
      v-model="iface"
      :options="ifaceOptions"
      label="Интерфейс"
      :disabled="isCascade"
      :tooltip="isCascade ? 'интерфейс каскадной группы задается при создании каскада' : ''"
    />

    <!-- кто ходит в группу (легаси egSrcMode/egSources/egSrcRouter) -->
    <template v-if="srcVisible">
      <UiSelect
        v-model="srcMode"
        :options="SRC_MODE_OPTIONS"
        label="Кто ходит в группу"
        :hint="SRC_MODE_HINTS[srcMode] || ''"
      />
      <template v-if="srcListVisible">
        <UiInput
          v-model="srcList"
          label="IP или CIDR, через запятую"
          placeholder="192.168.1.50, 192.168.2.0/24"
          mono
        />
        <UiSwitch
          v-model="srcRouter"
          label="Включая роутер"
          hint="его адреса добавятся в источники сами"
        />
      </template>
    </template>
    <UiHint
      v-else-if="!isCascade && !srcSupported"
      text="Подключённый MagiTrickle не поддерживает источники (нужен форк 0.8.2-rev3+). Обновите его - режимы появятся здесь."
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

