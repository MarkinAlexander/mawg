<script setup lang="ts">
import { UiBadge } from '../../components/ui';
import type { PoolMember } from '../../api/types';
import { nameSecret } from './poolSettingsLogic';

// секция «Состав пула» диалога настроек: протоколы/адреса участников;
// адресная разметка (secret) - статическая, по самому участнику
defineProps<{ members: PoolMember[] }>();
</script>

<template>
  <template v-if="members.length">
    <div class="sect">
      Состав пула (протоколы и адреса)
    </div>
    <div class="members">
      <div
        v-for="(m, i) in members"
        :key="i"
        class="member"
      >
        <UiBadge :text="m.proto + (m.detail ? '/' + m.detail : '')" />
        <!-- имя тоже может нести адрес (host|узел, файл-имя с хостом):
             скрываем вместе с endpoint (слово владельца 2026-10-17) -->
        <span
          class="mname"
          :title="m.name"
          :class="{ secret: nameSecret(m) }"
        >{{ m.name }}</span>
        <span
          class="mend"
          :class="{ secret: !!m.endpoint }"
        >{{ m.endpoint || '' }}</span>
        <UiBadge
          v-if="m.active"
          text="активен"
          variant="ok"
        />
        <UiBadge
          v-if="m.enabled === false"
          text="выключен"
          variant="muted"
        />
      </div>
    </div>
  </template>
</template>

<style scoped>
.sect {
  font-weight: 600;
  font-size: 13px;
  margin: 12px 0 4px;
}
.members {
  display: grid;
  gap: 4px;
  max-height: 180px;
  overflow: auto;
  font-size: 12px;
}
.member {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}
.mname {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mend {
  color: var(--dim);
  font-size: 11px;
}
</style>
