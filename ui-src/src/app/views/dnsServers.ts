// Чистые функции домена «резервные серверы MT» (каскад rev8+, health
// rev14+): тексты статусов, валидация адресов, петлезащита, сборка тел
// PUT /dns/upstreams. Refs остаются во вьюхе - сюда передаются параметрами.
import type { DnsState, DnsUpstream, DnsUpstreamsState } from '../../api/types';
import { durToSec, healthFieldValid } from './dnsLogic';

// черновик полей автопроверки (строки для UiInput)
export interface HealthDraft {
  enabled: boolean;
  interval: string;
  timeout: string;
  fail: string;
  restore: string;
  gap: string;
}

export function healthTextOf(h: DnsUpstreamsState['health'] | undefined): string {
  if (!h) return '';
  if (!h.enabled) return 'автопроверка выключена';
  const gap = h.restoreGap.replace('m0s', ' мин').replace('m', ' мин').replace('s', ' с');
  return `отключение при ${h.failCycles} сбоях подряд, восстановление после ${h.restoreCycles} успешных проверок, пауза между переключениями ${gap}`;
}

// tooltip сервера каскада; zoneProbeClean - последняя проба зоны через путь
// клиентов прошла (для зонных серверов поясняет «Шрёдингера»)
export function upTooltipOf(u: DnsUpstream, zoneProbeClean = false): string {
  const bits: string[] = [];
  if (u.reason) bits.push('причина: ' + u.reason);
  if (u.lastSwap && !u.lastSwap.startsWith('0001-')) bits.push('переключён: ' + new Date(u.lastSwap).toLocaleString());
  bits.push(`сбои подряд: ${u.fails}, успешные подряд: ${u.oks}`);
  bits.push('вердикт health-воркера MT (A-запрос имени зоны); после отвалов сервер возвращается только после N успешных проверок подряд - возможен разбег с пробой ниже');
  if (zoneProbeClean && !u.alive) {
    bits.push('проба через путь клиентов проходит - сервером можно пользоваться, health-воркер догонит');
  }
  return bits.join(' · ');
}

// сервер -> черновик (первая загрузка и после сохранения); правки
// пользователя не затираем (каскад перечитывается опросом каждые 10с)
export function healthDraftOf(h: DnsUpstreamsState['health']): HealthDraft {
  return {
    enabled: h.enabled,
    interval: String(durToSec(h.interval) ?? ''),
    timeout: String(durToSec(h.timeout) ?? ''),
    fail: String(h.failCycles),
    restore: String(h.restoreCycles),
    gap: String(durToSec(h.restoreGap) ?? ''),
  };
}

export function healthDirtyOf(h: DnsUpstreamsState['health'] | undefined, d: HealthDraft): boolean {
  if (!h) return false;
  return (
    d.enabled !== h.enabled ||
    +d.interval !== durToSec(h.interval) ||
    +d.timeout !== durToSec(h.timeout) ||
    +d.fail !== h.failCycles ||
    +d.restore !== h.restoreCycles ||
    +d.gap !== durToSec(h.restoreGap)
  );
}

export function healthValidOf(d: HealthDraft): boolean {
  return (
    healthFieldValid(d.interval, 5, 300) &&
    healthFieldValid(d.timeout, 1, 10) &&
    healthFieldValid(d.fail, 1, 20) &&
    healthFieldValid(d.restore, 1, 20) &&
    healthFieldValid(d.gap, 0, 3600)
  );
}

export function healthPutBody(d: HealthDraft): { health: { enabled: boolean; intervalSec: number; timeoutSec: number; failCycles: number; restoreCycles: number; restoreGapSec: number } } {
  return {
    health: {
      enabled: d.enabled,
      intervalSec: +d.interval,
      timeoutSec: +d.timeout,
      failCycles: +d.fail,
      restoreCycles: +d.restore,
      restoreGapSec: +d.gap,
    },
  };
}

export function dnsmasqTextOf(d: DnsState['dnsmasq'] | undefined): string {
  if (!d) return '';
  if (d.inMT) return d.servers.length > 1 ? `через MT + ещё (${d.servers.length})` : 'через MT (127.0.0.1#3553)';
  // чип не растёт вместе с длиной списка (слово владельца: десятки серверов
  // вылезали за карточку) - полный список рядом, по клику [?]
  if (d.servers.length === 1) return `свой сервер: ${d.servers[0]}`;
  if (d.servers.length) return `свои серверы: ${d.servers.length}`;
  return 'напрямую (серверы провайдера)';
}

// полный список своих серверов для [?] у чипа (UiHelp: pre-line + свой
// скролл, длинный список не ломает вёрстку)
export function dnsmasqServersHelpOf(d: DnsState['dnsmasq'] | undefined): string {
  if (!d || !d.servers.length) return '';
  return 'Свои серверы dnsmasq (заданы вручную):\n' + d.servers.map((s) => '· ' + s).join('\n');
}

export function upstreamsTextOf(m: DnsState['mt'] | undefined): string {
  if (!m) return '';
  if (m.upstreams?.length) return `${m.upstreams.join(' → ')} (первый - главный)`;
  return m.upstream || 'неизвестно';
}

// петлезащита (openwrt, dnsmasq.inMT): 127.0.0.1:53/свой IP:53 - это
// dnsmasq, который сам кормится от MT - петля уронит DNS. На Кинетике
// 127.0.0.1:53 легален (системный резолвер) - правило платформенное.
export function loopGuardOf(state: DnsState | null): boolean {
  return !!state && state.platform !== 'keenetic' && state.dnsmasq.inMT;
}

export function loopRiskOf(a: string, guard: boolean, self: string[]): boolean {
  if (!guard) return false;
  const t = a.trim();
  if (!t || t.startsWith('tls://') || t.startsWith('https://')) return false;
  let host = t;
  let port = '53';
  const idx = t.lastIndexOf(':');
  if (idx > 0) {
    host = t.slice(0, idx);
    port = t.slice(idx + 1);
  }
  host = host.replace(/^\[/, '').replace(/\]$/, '');
  if (port !== '53') return false;
  if (/^127\./.test(host) || host === '::1' || host === 'localhost') return true;
  return self.includes(host);
}

// адрес из редактора: пустой/кривой порт - ошибка текстом
export function addrErr(a: string): string | null {
  const t = a.trim();
  if (!t) return 'пустой адрес';
  const idx = t.lastIndexOf(':');
  if (idx > 0) {
    const port = Number(t.slice(idx + 1));
    if (!Number.isInteger(port) || port < 1 || port > 65535) return 'порт 1-65535';
  }
  return null;
}

export function editValidOf(list: string[] | null): boolean {
  return (list ?? []).length > 0 && (list ?? []).every((a) => addrErr(a) === null);
}

export function editDirtyOf(edit: string[] | null, addresses: string[]): boolean {
  if (edit === null) return false;
  return JSON.stringify(edit) !== JSON.stringify(addresses);
}

// перестановка соседей в списке (порядок = приоритет); вне границ - null
export function swappedAt(list: string[], i: number, d: number): string[] | null {
  const j = i + d;
  if (j < 0 || j >= list.length) return null;
  const c = [...list];
  [c[i], c[j]] = [c[j], c[i]];
  return c;
}

export function upstreamsPutBody(list: string[], riskyCount: number): { upstreams: string[]; confirm: boolean } {
  return { upstreams: list, confirm: riskyCount > 0 };
}

export function editConfirmAsk(risky: string[]): { title: string; text: string; okLabel: string } {
  return {
    title: 'Резервные серверы MT',
    text: risky.length
      ? `Осторожно: ${risky.join(', ')} - это служба DNS самого роутера (dnsmasq), а она сейчас направлена в MT. Такой адрес замыкает петлю (dnsmasq → MT → dnsmasq) и уронит DNS. Ставить только если точно понимаете, зачем. ` + 'Остальной список будет записан в настройки MT и заработает без перезапуска.'
      : 'Новый список будет записан в настройки MT и заработает без перезапуска. Первым используется верхний доступный сервер.',
    okLabel: risky.length ? 'Всё равно применить' : 'Сохранить',
  };
}
