<script setup lang="ts" generic="T">
import { ref } from 'vue'
import UiIcon from './UiIcon.vue'
import { dropIndex, isBeforeHalf } from './dnd'

// Таблица кита (база legacy table/th/td; dnd-семантика legacy attachDnD:
// draggable включается грипом по pointerdown, половина высоты цели = «до/
// после», индикаторы drop-before/drop-after). Ячейка по умолчанию -
// row[col.key]; слотом #cell-<key>="{ row }" - своя разметка.
export interface UiTableColumn {
  key: string
  label?: string
  width?: string
  align?: 'left' | 'right' | 'center'
}

const props = withDefaults(
  defineProps<{
    columns: UiTableColumn[]
    rows: T[]
    rowKey: (row: T) => string | number
    dnd?: boolean
    rowClass?: (row: T) => string
  }>(),
  { dnd: false, rowClass: undefined },
)

const emit = defineEmits<{ reorder: [from: number, to: number] }>()

// грип «взводит» draggable своей строки (семантика legacy pointerdown)
const armed = ref<string | number | null>(null)
const dragIndex = ref<number | null>(null)
const overIndex = ref<number | null>(null)
const before = ref(false)

function arm(key: string | number) {
  if (props.dnd) armed.value = key
}
function disarm() {
  armed.value = null
}

function onDragStart(e: DragEvent, index: number) {
  dragIndex.value = index
  // Firefox не начинает drag без данных в dataTransfer
  try {
    e.dataTransfer?.setData('text/plain', String(index))
  } catch {
    /* jsdom */
  }
}

function onDragOver(e: DragEvent, index: number) {
  if (dragIndex.value === null) return
  e.preventDefault()
  const rect = (e.currentTarget as HTMLElement).getBoundingClientRect()
  overIndex.value = index
  before.value = isBeforeHalf(rect, e.clientY)
}

function onDrop(e: DragEvent, index: number) {
  e.preventDefault()
  const from = dragIndex.value
  if (from !== null && from !== index) {
    const to = dropIndex(from, index, before.value)
    if (to !== from) emit('reorder', from, to)
  }
  dragIndex.value = null
  overIndex.value = null
}

function onDragEnd() {
  dragIndex.value = null
  overIndex.value = null
  armed.value = null
}

function rowMods(index: number) {
  return {
    dragging: dragIndex.value === index,
    'drop-before': overIndex.value === index && before.value,
    'drop-after': overIndex.value === index && !before.value,
  }
}
</script>

<template>
  <table class="uitable">
    <thead>
      <tr>
        <th
          v-if="props.dnd"
          class="grip-col"
        />
        <th
          v-for="c in props.columns"
          :key="c.key"
          :style="{ width: c.width, textAlign: c.align }"
        >
          {{ c.label }}
        </th>
      </tr>
    </thead>
    <tbody>
      <tr
        v-for="(row, i) in props.rows"
        :key="props.rowKey(row)"
        :class="[props.rowClass?.(row), rowMods(i)]"
        :draggable="props.dnd && armed === props.rowKey(row)"
        @dragstart="onDragStart($event, i)"
        @dragover="onDragOver($event, i)"
        @drop="onDrop($event, i)"
        @dragend="onDragEnd"
      >
        <td
          v-if="props.dnd"
          class="grip-col"
          style="width: 18px"
        >
          <span
            class="grip"
            title="переместить перетаскиванием"
            @pointerdown="arm(props.rowKey(row))"
            @pointerup="disarm"
          >
            <UiIcon name="grip" />
          </span>
        </td>
        <td
          v-for="c in props.columns"
          :key="c.key"
          :style="{ textAlign: c.align }"
        >
          <slot
            :name="`cell-${c.key}`"
            :row="row"
            :index="i"
          >
            {{ (row as Record<string, unknown>)[c.key] }}
          </slot>
        </td>
      </tr>
    </tbody>
  </table>
</template>

<style scoped>
.uitable {
  width: 100%;
  border-collapse: collapse;
  font-size: 12.5px;
  margin-top: 8px;
}
th,
td {
  text-align: left;
  padding: 4px 6px;
  border-bottom: 1px solid var(--border);
  word-break: break-all;
}
th {
  color: var(--dim);
  font-weight: normal;
}
.grip {
  color: var(--dim);
  cursor: grab;
  display: inline-flex;
  padding: 2px;
  vertical-align: middle;
}
.grip:active {
  cursor: grabbing;
}
tr.dragging td {
  opacity: 0.4;
}
tr.drop-before td {
  border-top: 2px solid var(--accent);
}
tr.drop-after td {
  border-bottom: 2px solid var(--accent);
}
</style>
