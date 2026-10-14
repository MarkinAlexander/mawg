<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { UiBadge, UiCard, UiHint } from '../../components/ui'
import { useStatus, usePools } from '../../stores/status'
import { useGroups } from '../../stores/groups'
import { useIfaces } from '../../stores/ifaces'
import { useBundles } from '../../stores/bundles'
import type { MtState } from '../../stores/status'

// Обзор (44-дока §6) - короткая выжимка: состояние MT и плитки-счётчики
// со ссылками на вкладки. Пулы живут на «Интерфейсах» (49-док), LAN-клиенты
// - в Настройках, мастера настроек встанут сюда в Фазе 5 (46-дока §2).

const { state: status } = useStatus();
const { pools } = usePools();
const groups = useGroups();
const ifaces = useIfaces();
const bundles = useBundles();

onMounted(() => {
  groups.start(30000);
  ifaces.start();
  bundles.start();
  void groups.loadBackups();
});
onBeforeUnmount(() => {
  groups.stop();
  ifaces.stop();
  bundles.stop();
});

const MT_LABEL: Record<MtState, string> = {
  checking: 'проверяю...',
  yes: 'доступен',
  no: 'недоступен',
};
const MT_BADGE: Record<MtState, 'ok' | 'warn' | 'err'> = {
  checking: 'warn',
  yes: 'ok',
  no: 'err',
};

const rulesCount = computed(() =>
  groups.state.groups.reduce((n, g) => n + (g.rules?.length || 0), 0));
const cascadesCount = computed(() =>
  groups.state.groups.filter((g) => g.cascade).length);

// 1 правило/2 правила/5 правил
function plural(n: number, one: string, few: string, many: string): string {
  const m10 = n % 10;
  const m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return `${n} ${one}`;
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return `${n} ${few}`;
  return `${n} ${many}`;
}

const tiles = computed(() => {
  const rules = rulesCount.value;
  const casc = cascadesCount.value;
  const groupSub = `${plural(rules, 'правило', 'правила', 'правил')}${casc ? `, ${plural(casc, 'каскад', 'каскада', 'каскадов')}` : ''}`;
  return [
    {
      to: '/next/groups', label: 'Группы правил', count: groups.state.groups.length,
      sub: groupSub,
    },
    {
      to: '/next/interfaces', label: 'Пулы', count: pools.value.length,
      sub: 'конфиги AWG, слоты, ротация',
    },
    {
      to: '/next/interfaces', label: 'Интерфейсы', count: ifaces.state.list.length,
      sub: 'подключения роутера и наборы',
    },
    {
      to: '/next/backups', label: 'Резервные копии', count: groups.state.backups.list.length,
      sub: 'полный архив, правила, конфиг MT',
    },
  ];
});
</script>

<template>
  <UiCard
    id="card-overview"
    title="Обзор"
    subtitle="сводка: детали живут на вкладках; поиск по настройкам - лупа в шапке (Ctrl+K)"
  >
    <div class="mtrow">
      <UiBadge
        :text="'magitrickle: ' + MT_LABEL[status.mt]"
        :variant="MT_BADGE[status.mt]"
      />
      <span
        v-if="status.data"
        class="meta"
      >{{ status.data.platform }} · mawg {{ status.data.version }}</span>
      <span
        v-else
        class="meta"
      >{{ status.error || 'загрузка...' }}</span>
    </div>

    <div class="tiles">
      <RouterLink
        v-for="t in tiles"
        :key="t.label"
        :to="t.to"
        class="tile"
      >
        <span class="count">{{ t.count }}</span>
        <span class="tlabel">{{ t.label }}</span>
        <span class="tsub">{{ t.sub }}</span>
      </RouterLink>
    </div>

    <UiHint text="Журнал событий - вкладка «Система». Доступ и LAN-клиенты - «Настройки». Будущие мастера настройки появятся здесь (Фаза 5)." />
  </UiCard>
</template>

<style scoped>
.mtrow {
  display: flex;
  gap: 10px;
  align-items: center;
  flex-wrap: wrap;
}
.meta {
  color: var(--dim);
  font-size: 12px;
}
.tiles {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 10px;
}
.tile {
  display: flex;
  flex-direction: column;
  gap: 2px;
  background: var(--panel2);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 10px 12px;
  text-decoration: none;
  color: var(--text);
}
.tile:hover {
  border-color: var(--accent);
}
.count {
  font-size: 22px;
  font-weight: 600;
}
.tlabel {
  font-size: 13px;
}
.tsub {
  color: var(--dim);
  font-size: 11px;
}
</style>
