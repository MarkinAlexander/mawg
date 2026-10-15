<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UiButton, UiDialog, UiHint, UiSelect, type UiSelectOption, UiTextarea } from '../../components/ui'
import { api } from '../../api/client'
import { useGroups } from '../../stores/groups'
import { usePools } from '../../stores/status'
import { useToast } from '../../stores/toast'
import { cascadeOptions, wireCascadeSel } from './groupsLogic'

// Диалог каскада (легаси fillCascadeSelects 2067 + csCreate 2090): источник
// и путь из пулов и интерфейсов (выбранное в одном дизейблится в другом),
// hosts - только при iface-источнике, POST /cascades. С Ф5-полировки
// адреса iface-источника автоподставляются (GET /cascades/iface-endpoints:
// Кинетик отдаёт remote-endpoint-address пиров; баг владельца - каскад с
// nwg7 требовал адреса от руки).
const open = defineModel<boolean>('open', { default: false });

const { state, createCascade } = useGroups();
const { pools } = usePools();
const { push } = useToast();

const source = ref('');
const via = ref('');
const hosts = ref('');
const hostsAuto = ref(false);
const busy = ref(false);

const allOpts = computed(() => cascadeOptions(pools.value, state.ifaces));
const srcUi = computed<UiSelectOption[]>(() =>
  wireCascadeSel(allOpts.value, via.value, source.value).opts.map((o) => ({
    value: o.ref,
    label: o.label,
    hint: o.disabled ? 'занят в соседнем списке' : undefined,
  })));
const viaUi = computed<UiSelectOption[]>(() =>
  wireCascadeSel(allOpts.value, source.value, via.value).opts.map((o) => ({
    value: o.ref,
    label: o.label,
    hint: o.disabled ? 'занят в соседнем списке' : undefined,
  })));

const hostsVisible = computed(() => source.value.startsWith('iface:'));

// автоподстановка адресов интерфейса-источника; ручной ввод остаётся
// правкой поверх (можно дополнить/исправить)
watch(hostsVisible, async (v) => {
  if (!v) {
    hostsAuto.value = false;
    return;
  }
  try {
    const r = await api<{ hosts: string[] }>('GET', `/cascades/iface-endpoints?source=${encodeURIComponent(source.value)}`);
    if ((r.hosts || []).length) {
      hosts.value = r.hosts.join('\n');
      hostsAuto.value = true;
    }
  } catch {
    // платформа не умеет/нет пиров - заполняет владелец руками
    hostsAuto.value = false;
  }
});

// смена источника: путь уезжает с занятого варианта на первый разрешённый
// (легаси fillCascadeSel 2059-2062); и наоборот
function onSource(v: string): void {
  source.value = v;
  via.value = wireCascadeSel(allOpts.value, source.value, via.value).value;
}
function onVia(v: string): void {
  via.value = v;
  source.value = wireCascadeSel(allOpts.value, via.value, source.value).value;
}

watch(open, (v) => {
  if (!v) return;
  hosts.value = '';
  hostsAuto.value = false;
  source.value = '';
  via.value = '';
  onSource(wireCascadeSel(allOpts.value, '', '').value);
});

async function save(): Promise<void> {
  if (!source.value || !via.value) {
    push('Выберите источник и путь', true);
    return;
  }
  if (source.value === via.value) {
    push('Источник и путь совпадают', true);
    return;
  }
  busy.value = true;
  try {
    await createCascade({ source: source.value, via: via.value, hosts: hosts.value.split(/\n+/).map((x) => x.trim()).filter(Boolean) });
    open.value = false;
    push('Каскад создан и включен');
  } catch (e) {
    push(e instanceof Error ? e.message : String(e), true);
  }
  busy.value = false;
}
</script>

<template>
  <UiDialog
    v-model:open="open"
    title="Каскад"
    :busy="busy"
    width="440px"
  >
    <UiHint text="Создается служебная группа: трафик к эндпоинтам источника направляется через путь-посредник. Включается и выключается обычным тумблером группы; при ротации источника адреса добавляются сами." />
    <UiSelect
      :model-value="source"
      :options="srcUi"
      label="Что гоняем (источник эндпоинтов)"
      @update:model-value="onSource"
    />
    <UiHint
      v-if="hostsAuto"
      text="Адреса определены автоматически из интерфейса - можно дополнить или исправить."
    />
    <UiTextarea
      v-if="hostsVisible"
      v-model="hosts"
      label="Адреса или домены его эндпоинтов (по одному в строке)"
      :rows="3"
      placeholder="8.39.125.1"
    />
    <UiSelect
      :model-value="via"
      :options="viaUi"
      label="Через что (путь)"
      @update:model-value="onVia"
    />
    <template #footer>
      <UiButton
        :disabled="busy"
        @click="open = false"
      >
        Отмена
      </UiButton>
      <UiButton
        variant="primary"
        :busy="busy"
        @click="save"
      >
        Создать каскад
      </UiButton>
    </template>
  </UiDialog>
</template>
