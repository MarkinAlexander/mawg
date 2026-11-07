// Чистые функции домена «правила автопереключения L1-L5»: черновик
// уровней, сборка тела PUT /dns/policy и тексты подтверждений.
import type { DnsCascadePolicy } from '../../api/types';
import { linesOf } from './dnsLogic';

// шифрованные резолверы (MT rev11+): адрес сразу канонический, кнопка
// дописывает строку в основные; на rev10- не показываем
export const dohPresets = [
  { label: 'Google DoH', addr: 'https://dns.google' },
  { label: 'Cloudflare DoH', addr: 'https://1.1.1.1/dns-query' },
  { label: 'AdGuard DoH', addr: 'https://94.140.14.14/dns-query' },
  { label: 'Google DoT', addr: 'tls://8.8.8.8' },
  { label: 'Cloudflare DoT', addr: 'tls://1.1.1.1' },
];

// черновик уровней (v-model полей карточки)
export interface PolicyDraft {
  enabled: boolean;
  primary: string;
  tunnel: string;
  fallback: string;
  yandex: boolean;
  provider: boolean;
}

export function policyDraftOf(p: DnsCascadePolicy): PolicyDraft {
  return {
    enabled: !!p.enabled,
    primary: (p.primary || []).join('\n'),
    tunnel: p.tunnel || '',
    fallback: (p.fallback || []).join('\n'),
    yandex: !!p.yandex,
    provider: !!p.provider,
  };
}

// «Сохранить и применить» доступна только после правок: без изменений
// кнопка недоступна (слово владельца 2026-10-18: «просто так доступна,
// хотя я ничего не правил, это не логично»)
export function policyDirtyOf(p: DnsCascadePolicy | undefined, d: PolicyDraft): boolean {
  if (!p) return false;
  return d.enabled !== !!p.enabled
    || d.primary !== (p.primary || []).join('\n')
    || d.tunnel !== (p.tunnel || '')
    || d.fallback !== (p.fallback || []).join('\n')
    || d.yandex !== !!p.yandex
    || d.provider !== !!p.provider;
}

export function policyBodyOf(d: PolicyDraft, riskyCount: number) {
  return {
    enabled: d.enabled,
    primary: linesOf(d.primary),
    tunnel: d.tunnel,
    fallback: linesOf(d.fallback),
    yandex: d.yandex,
    provider: d.provider,
    confirm: riskyCount > 0,
  };
}

export type PolicyBody = ReturnType<typeof policyBodyOf>;

// включённой политике нужен хотя бы один уровень (L1/L3/L4/L5)
export function policyMissingLevels(body: PolicyBody): boolean {
  return body.enabled && body.primary.length === 0 && body.fallback.length === 0 && !body.yandex && !body.provider;
}

export function policyConfirmAsk(body: PolicyBody): { title: string; text: string; okLabel: string } {
  return {
    title: 'Сохранить правила автопереключения',
    text: (body.tunnel ? `Для туннельного уровня будет настроено правило обхода через «${body.tunnel}». ` : '')
      + 'Цепочка серверов MT будет переписана: основные → запасные'
      + (body.yandex ? ' → Яндекс' : '')
      + (body.provider ? ' → провайдер' : '')
      + '. Само переключение при сбоях делает MT - автоматически и незаметно.',
    okLabel: 'Сохранить и применить',
  };
}

// пресет дописывается в основные без дубля
export function withPreset(text: string, addr: string): string {
  const cur = linesOf(text);
  if (!cur.includes(addr)) cur.push(addr);
  return cur.join('\n');
}
