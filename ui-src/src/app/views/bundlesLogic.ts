// Наборы интерфейсов: когда участник считается живым и гистерезис
// переключения. Чистые функции без DOM - тестируются на любой платформе.
import { computed, ref, watch } from 'vue'
import type { BundleRules, BundleView } from '../../api/types'
import { useBundles } from '../../stores/bundles'
import { useToast } from '../../stores/toast'
import { cyclesValid } from './groupsLogic'

export type HealthSource = BundleRules['healthSource'];

export const HEALTH_SOURCE_OPTIONS: { value: HealthSource; label: string }[] = [
  { value: 'probe', label: 'по ответу на пробу' },
  { value: 'state', label: 'по состоянию' },
];

// бейдж участника набора: текущий - зелёный (жёлтый, если сам не жив),
// живой запасной - нейтральный, мёртвый - красный; в подсказке - причина
export function memberBadge(b: BundleView, m: string): { variant: 'ok' | 'warn' | 'err' | 'muted'; tip: string } {
  const device = (b.memberDevices || {})[m] || m;
  const hs = b.memberHealth?.[m];
  const tip = hs ? `${m} - ${hs.pending ? 'проверяется' : hs.ok ? 'жив' : 'не жив'}${hs.note && !hs.pending ? `: ${hs.note}` : ''}` : m;
  const dead = !!hs && !hs.ok && !hs.pending;
  if (b.currentMember === device) return { variant: dead ? 'warn' : 'ok', tip };
  return { variant: dead ? 'err' : 'muted', tip };
}

export function useBundleRulesForm() {
  const bundles = useBundles();
  const { push } = useToast();
  const source = ref<HealthSource>('probe');
  const fail = ref('');
  const restore = ref('');
  const saving = ref(false);
  watch(() => bundles.state.rules, (r) => {
    if (!r) return;
    source.value = r.healthSource;
    fail.value = String(r.failCycles);
    restore.value = String(r.restoreCycles);
  }, { immediate: true, deep: true });
  const changed = computed(() => {
    const r = bundles.state.rules;
    return !!r && cyclesValid(fail.value, restore.value)
      && (source.value !== r.healthSource || +fail.value !== r.failCycles || +restore.value !== r.restoreCycles);
  });

  async function save(): Promise<void> {
    if (!changed.value || saving.value) return;
    saving.value = true;
    try {
      const r = await bundles.saveRules({ healthSource: source.value, failCycles: +fail.value, restoreCycles: +restore.value });
      const how = HEALTH_SOURCE_OPTIONS.find((o) => o.value === r.healthSource)?.label ?? r.healthSource;
      push(`Наборы: участник живой ${how}, уход после ${r.failCycles} проверок без ответа, возврат после ${r.restoreCycles} с ответом`);
    } catch (e) {
      push(e instanceof Error ? e.message : String(e), true);
    }
    saving.value = false;
  }
  return { source, fail, restore, saving, changed, save };
}
