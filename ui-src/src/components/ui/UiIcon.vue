<script setup lang="ts">
import { computed } from 'vue'
import { ICONS, type IconName } from './icons'

const props = defineProps<{
  name: IconName
  title?: string
}>()

const svg = computed(() => ICONS[props.name])
</script>

<template>
  <span
    class="uiicon"
    :title="props.title"
    v-html="svg"
  />
</template>

<style scoped>
.uiicon {
  display: inline-flex;
  vertical-align: middle;
  line-height: 0;
}
</style>
