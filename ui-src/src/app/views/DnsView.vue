<script setup lang="ts">
import { UiBadge, UiCard, UiHint } from '../../components/ui'

// Ф5 §6 (44-дока): вкладка DNS - новый раздел, легаси-аналога не было.
// Задано на будущее: статус ремапа DNS роутера (MT_DNSOR), сторонние
// upstream'ы (DoH/DoT, multi-upstream с фолбэком - спека форка 43-док §5),
// DoH-гайд. Пока - только бейдж «в разработке» (слово владельца).
</script>

<template>
  <UiCard
    title="DNS"
    subtitle="управление DNS: ремап, сторонние upstream'ы, туннелирование"
  >
    <template #actions>
      <UiBadge
        variant="warn"
        text="раздел в разработке"
      />
    </template>
    <p class="dnslead">
      Раздел в разработке - легаси-аналога у него не было. Планируется:
    </p>
    <ul class="dnslist">
      <li>статус ремапа DNS роутера (MT_DNSOR);</li>
      <li>сторонние upstream'ы: DoH/DoT, multi-upstream с фолбэком;</li>
      <li>DoH-гайд.</li>
    </ul>
    <UiHint text="Сейчас DNS-правила управляются через группы правил: пресет «DNS через туннель» в шаблонах групп уже работает." />
  </UiCard>
</template>

<style scoped>
.dnslead {
  margin: 0 0 6px;
}
.dnslist {
  margin: 0 0 10px;
  padding-left: 20px;
}
.dnslist li {
  margin: 2px 0;
}
</style>
