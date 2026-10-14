<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { UiDialog, UiHint, UiInput } from '../components/ui'
import { TABS } from './tabs'
import { filterPlaces, type SearchPlace } from './search'
import { hlSegs } from './views/groupsLogic'

// Поиск по настройкам (46-док §1, первый пункт Фазы 4): лупа в шапке и
// Ctrl+K (вешает NextLayout) открывают диалог, фильтрация по подстроке
// без фаззи; Enter/клик - переход на вкладку + скролл к карточке-якорю
// (id на UiCard вьюх). Повторный поиск на той же вкладке - просто скролл.
const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ 'update:open': [value: boolean] }>()

const router = useRouter()
const q = ref('')
const sel = ref(0)

const results = computed(() => filterPlaces(q.value))

// подстрока, по которой реально фильтровали - для подсветки в title
const normQ = computed(() => q.value.trim().toLowerCase())
function segs(place: SearchPlace) {
  return hlSegs(place.title, normQ.value)
}

// метка вкладки справа в строке результата (route = /next/{id})
function tabLabel(place: SearchPlace): string {
  return TABS.find((t) => `/next/${t.id}` === place.route)?.label ?? place.route
}

const box = ref<HTMLDivElement | null>(null)
const listEl = ref<HTMLDivElement | null>(null)

// при открытии - чистый запрос и фокус в инпут
watch(
  () => props.open,
  async (v) => {
    if (!v) return
    q.value = ''
    sel.value = 0
    await nextTick()
    box.value?.querySelector('input')?.focus()
  },
)

// запрос меняется - выбор к первой строке
watch(q, () => {
  sel.value = 0
})

function scrollSel() {
  listEl.value?.querySelector('.srow.sel')?.scrollIntoView({ block: 'nearest' })
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    if (sel.value < results.value.length - 1) sel.value += 1
    scrollSel()
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    if (sel.value > 0) sel.value -= 1
    scrollSel()
  } else if (e.key === 'Enter') {
    e.preventDefault()
    void go(results.value[sel.value])
  }
}

async function go(place: SearchPlace | undefined) {
  if (!place) return
  emit('update:open', false)
  await router.push(place.route)
  // ленивая вьюха монтируется после навигации - даём дорисоваться
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 0))
  if (place.anchor) {
    document.getElementById(place.anchor)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }
}
</script>

<template>
  <UiDialog
    :open="props.open"
    title="Поиск по настройкам"
    width="560px"
    @update:open="emit('update:open', $event)"
  >
    <div
      ref="box"
      class="sbox"
      @keydown="onKeydown"
    >
      <UiInput
        v-model="q"
        bare
        placeholder="начните вводить: каскад, wan, rci..."
      />
      <div
        ref="listEl"
        class="slist"
      >
        <button
          v-for="(p, i) in results"
          :key="p.title"
          type="button"
          class="srow"
          :class="{ sel: i === sel }"
          @click="go(p)"
          @mousemove="sel = i"
        >
          <span class="sttl"><template
            v-for="(s, j) in segs(p)"
            :key="j"
          ><mark v-if="s.hit">{{ s.text }}</mark><template v-else>{{ s.text }}</template></template></span>
          <span class="stab">{{ tabLabel(p) }}</span>
        </button>
        <div
          v-if="!results.length"
          class="sempty"
        >
          Ничего не найдено
        </div>
      </div>
      <div class="sfoot">
        совпадений: {{ results.length }}
      </div>
      <UiHint text="стрелки - выбор, Enter - перейти, Esc - закрыть" />
    </div>
  </UiDialog>
</template>

<style scoped>
.sbox {
  display: grid;
  gap: 8px;
}
.slist {
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 55vh;
  overflow-y: auto;
}
.srow {
  display: flex;
  align-items: baseline;
  gap: 8px;
  width: 100%;
  text-align: left;
  background: none;
  border: none;
  border-radius: 6px;
  padding: 7px 8px;
  color: var(--text);
  cursor: pointer;
  font-size: 13px;
}
.srow.sel {
  background: var(--panel2);
}
.sttl {
  flex: 1;
  min-width: 0;
}
/* легаси-подсветка поиска, как .gname mark в группах */
.sttl mark {
  background: rgba(217, 160, 50, 0.35);
  color: inherit;
}
.stab {
  flex: none;
  color: var(--dim);
  font-size: 11px;
}
.sempty {
  color: var(--dim);
  padding: 8px;
}
.sfoot {
  color: var(--dim);
  font-size: 12px;
}
</style>
