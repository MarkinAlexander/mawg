<script setup lang="ts">
import { UiHint } from '../../components/ui'
import { usePools } from '../../stores/status'
import PoolCard from './PoolCard.vue'

// Фаза 3.5а: карточки пулов (IA 44-доки §6: Обзор = статус, пулы, LAN-клиенты,
// баннеры - LAN-клиенты и баннеры позже). Статус опрашивает NextLayout (5с),
// карточки просто отображают. Кнопка «+ интерфейс» и шестерёнка настроек -
// Ф3.5б (диалоги создания/настроек).
const { pools } = usePools();
</script>

<template>
  <UiHint
    v-if="!pools.length"
    text="Пулов нет. Создайте первый, например proton."
  />
  <PoolCard
    v-for="p in pools"
    :key="p.name"
    :pool="p"
  />
</template>
