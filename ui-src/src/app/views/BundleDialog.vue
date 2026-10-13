<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiBadge, UiButton, UiDialog, UiHint, UiInput, UiSelect, type UiSelectOption, UiSwitch } from '../../components/ui'
import { useBundles } from '../../stores/bundles'
import { useGroups } from '../../stores/groups'
import { useIfaces } from '../../stores/ifaces'
import { usePools } from '../../stores/status'
import { useToast } from '../../stores/toast'
import type { BundleView } from '../../api/types'

// Диалог набора (легаси openBundleDlg/bdSave 2342-2384): имя (при правке
// фиксировано), участники по приоритету (выше/ниже/убрать + добавление из
// пулов и внешних интерфейсов), группы MagiTrickle тумблерами.
const props = defineProps<{ editing: BundleView | null }>();
const open = defineModel<boolean>('open', { default: false });

const a = useBundles();
const { state: groupsState, refresh: refreshGroups } = useGroups();
const { pools } = usePools();
const { state: ifaces } = useIfaces();
const { push } = useToast();

const name = ref('');
const members = ref<string[]>([]);
const picked = ref('');
const busy = ref(false);

// легаси bundleCandidates: пулы + внешние интерфейсы
const candidates = computed(() => {
  const out: { id: string; label: string }[] = [];
  for (const p of pools.value) out.push({ id: p.name, label: `${p.name} (пул)` });
  for (const i of ifaces.list) {
    if (i.mode === 'external') {
      out.push({ id: i.device, label: i.device + (i.description && i.description !== i.device ? ` - ${i.description}` : '') + ' (внешний)' });
    }
  }
  return out;
});
const addOptions = computed<UiSelectOption[]>(() => {
  const used = new Set(members.value);
  const list = candidates.value.filter((c) => !used.has(c.id));
  return list.length ? list.map((c) => ({ value: c.id, label: c.label })) : [{ value: '', label: 'нет доступных участников' }];
});
const memberLabel = (id: string) => candidates.value.find((c) => c.id === id)?.label || id;

function addMember(): void {
  if (picked.value && !members.value.includes(picked.value)) {
    members.value.push(picked.value);
    picked.value = '';
  }
}
function move(i: number, delta: number): void {
  const j = i + delta;
  if (j < 0 || j >= members.value.length) return;
  [members.value[i], members.value[j]] = [members.value[j], members.value[i]];
}
function removeAt(i: number): void {
  members.value.splice(i, 1);
}

const inBundle = ref<Set<string>>(new Set());
watch(open, async (v) => {
  if (!v) return;
  name.value = props.editing ? props.editing.name : '';
  members.value = props.editing ? [...(props.editing.members || [])] : [];
  picked.value = '';
  inBundle.value = new Set(props.editing?.groups || []);
  void refreshGroups(true); // легаси читает MT_GROUPS - свежий список групп
});

function toggleGroup(id: string): void {
  const next = new Set(inBundle.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  inBundle.value = next;
}

async function save(): Promise<void> {
  const nm = name.value.trim() || props.editing?.name || '';
  if (!nm) {
    push('Укажите имя', true);
    return;
  }
  if (!members.value.length) {
    push('Добавьте хотя бы одного участника', true);
    return;
  }
  const groups = [...inBundle.value];
  if (!groups.length) {
    push('Выберите хотя бы одну группу', true);
    return;
  }
  await withBusy(async () => {
    try {
      await a.save(props.editing?.name || null, { name: nm, members: [...members.value], groups });
      open.value = false;
    } catch (e) {
      push(e instanceof Error ? e.message : String(e), true);
    }
  });
}

async function withBusy(fn: () => Promise<void>): Promise<void> {
  if (busy.value) return;
  busy.value = true;
  try {
    await fn();
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <UiDialog
    v-model:open="open"
    :title="editing ? 'Изменить набор' : 'Новый набор'"
    :busy="busy"
    width="480px"
  >
    <UiInput
      v-model="name"
      label="Имя"
      :disabled="!!editing"
      tooltip="Латиница или кириллица, цифры и дефисы, до 24 символов"
    />
    <div class="sect">
      Участники по приоритету (первый = основной)
    </div>
    <div class="memlist">
      <div
        v-for="(m, i) in members"
        :key="m"
        class="memrow"
      >
        <UiBadge :text="memberLabel(m)" />
        <span class="sp" />
        <UiButton
          variant="icon"
          tooltip="выше"
          :disabled="i === 0"
          @click="move(i, -1)"
        >
          ↑
        </UiButton>
        <UiButton
          variant="icon"
          tooltip="ниже"
          :disabled="i === members.length - 1"
          @click="move(i, 1)"
        >
          ↓
        </UiButton>
        <UiButton
          variant="danger"
          @click="removeAt(i)"
        >
          убрать
        </UiButton>
      </div>
      <UiHint
        v-if="!members.length"
        text="Участников нет - добавьте пул или внешний интерфейс."
      />
    </div>
    <UiSelect
      v-model="picked"
      :options="addOptions"
      label="добавить участника"
    >
      <template #action>
        <UiButton
          :disabled="!picked"
          @click="addMember"
        >
          добавить
        </UiButton>
      </template>
    </UiSelect>
    <div class="sect">
      Группы MagiTrickle в наборе
    </div>
    <div class="grouplist">
      <div
        v-for="g in groupsState.groups"
        :key="g.id"
        class="grourow"
      >
        <UiSwitch
          :model-value="inBundle.has(g.id)"
          tooltip="включить группу в набор"
          @update:model-value="() => toggleGroup(g.id)"
        />
        <span class="gname">{{ g.name }} <span class="giface">({{ g.interface }})</span></span>
      </div>
      <UiHint
        v-if="!groupsState.groups.length"
        :text="groupsState.available ? 'Групп нет.' : 'Группы MagiTrickle недоступны'"
      />
    </div>
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
.sect {
  font-weight: 600;
  font-size: 13px;
  margin: 12px 0 4px;
}
.memlist {
  display: grid;
  gap: 2px;
  margin-bottom: 4px;
}
.memrow {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 3px 0;
}
.sp {
  flex: 1;
}
.grouplist {
  display: grid;
  gap: 2px;
  margin-top: 4px;
}
.grourow {
  display: flex;
  gap: 10px;
  align-items: center;
  padding: 2px 0;
  font-size: 13px;
}
.giface {
  color: var(--dim);
}
</style>
