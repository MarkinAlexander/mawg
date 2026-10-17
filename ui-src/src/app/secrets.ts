// Что именно скрывает приватный режим (62-док + слово владельца 2026-10-15):
// не «места в DOM», а значения - адреса эндпоинтов из конфигов пулов
// (host:port). Домены правил, публичные DNS (1.1.1.1, 8.8.8.8), пробы,
// счётчики и строки журнала без этих адресов остаются видимыми. Секреты
// (ключи, подписки, токены, доступ к панели) помечены статически.
import { computed } from 'vue'
import { usePools } from '../stores/status'
import type { PoolView } from '../api/types'

// хост из 'host:port' / 'host/32' / '*.host' / '*.cdn-*'; IPv6 целиком не
// разбираем (берём до первого ':', кроме [v6]:port - не наш случай)
export function hostOf(v: string): string {
  let s = v.trim().toLowerCase();
  if (s.startsWith('*.')) s = s.slice(2);
  else if (s.startsWith('*')) s = s.slice(1);
  const slash = s.indexOf('/');
  if (slash > 0) s = s.slice(0, slash);
  const colon = s.indexOf(':');
  if (colon > 0) s = s.slice(0, colon);
  return s;
}

// все хосты-эндпоинты пулов: активные + все конфиги ротации
export function collectHosts(pools: PoolView[]): Set<string> {
  const out = new Set<string>();
  for (const p of pools) {
    for (const e of [p.activeEndpoint, ...(p.configs || []).map((c) => c.endpoint || '')]) {
      const h = hostOf(e);
      // 'узлов: 4' и прочие не-адреса отсекаются: у хоста есть точка
      if (h.includes('.')) out.add(h);
    }
  }
  return out;
}

export function endpointSecretIn(hosts: Set<string>, v: string | undefined): boolean {
  if (!v) return false;
  const h = hostOf(v);
  return hosts.has(h);
}

// правило скрываем, только если его значение - адрес/подсеть эндпоинта
// из конфигов (hosts каскадов); домены и чужие подсети - видны
export function ruleSecretIn(hosts: Set<string>, rule: string): boolean {
  if (!rule) return false;
  const h = hostOf(rule);
  if (!h) return false;
  return hosts.has(h);
}

// строка (журнал, ошибка пробы) скрывается, только если содержит
// адрес из конфигов
export function lineSecretIn(hosts: Set<string>, text: string): boolean {
  if (!text) return false;
  for (const h of hosts) {
    if (text.includes(h)) return true;
  }
  return false;
}

// в тексте есть IPv4-адрес (203.0.113.7|узел и т.п.): приватный режим
// прячет такие строки и без знания хостов. Публичные резолверы
// (8.8.8.8, 1.1.1.1) в тегах узлов не встречаются, так что не задеваем
// слово владельца «публичные DNS видны» (слово 2026-10-17: тег узла
// выдаёт адрес мимо сокрытия).
const IPV4_RE = /\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b/;
export function addrSecretIn(text: string | undefined): boolean {
  return !!text && IPV4_RE.test(text);
}

export function useSecrets() {
  const { pools } = usePools();
  const hosts = computed(() => collectHosts(pools.value));
  return {
    hosts,
    endpoint: (v: string | undefined) => endpointSecretIn(hosts.value, v),
    rule: (r: string) => ruleSecretIn(hosts.value, r),
    line: (t: string) => lineSecretIn(hosts.value, t),
  };
}
