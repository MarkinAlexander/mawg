// Чистые функции домена «собственные серверы Keenetic» (71-док): сборка
// строк списков из running-config и совет по усилению DNS роутера.
import type { DnsProbeRow, KeeneticOwn } from '../../api/types';

export interface KeeneticOwnRow {
  key: string;
  label: string;
  sub: string;
  kind?: string;
  server?: string;
  sni?: string;
}

export function ownRowsOf(own: KeeneticOwn | null): KeeneticOwnRow[] {
  if (!own) return [];
  const rows: KeeneticOwnRow[] = [];
  if (!(own.doh || []).length) rows.push({ key: 'empty-doh', label: 'DoH', sub: 'записей не найдено' });
  for (const d of own.doh || []) {
    rows.push({ key: `doh|${d.uri}`, label: d.uri, sub: d.format ? `формат ${d.format}` : '', kind: 'doh', server: d.uri });
  }
  if (!(own.dot || []).length) rows.push({ key: 'empty-dot', label: 'DoT', sub: 'записей не найдено' });
  for (const t of own.dot || []) {
    const server = t.port ? `${t.address}:${t.port}` : `${t.address}:853`;
    rows.push({ key: `dot|${server}|${t.sni || ''}`, label: server, sub: t.sni ? `sni ${t.sni}` : '', kind: 'dot', server, sni: t.sni });
  }
  if (!(own.udp || []).length) rows.push({ key: 'empty-udp', label: 'Простые (UDP)', sub: 'записей не найдено' });
  for (const u of own.udp || []) {
    for (const srv of u.servers) rows.push({ key: `udp|${srv}`, label: srv, sub: `подключение ${u.interface}`, kind: 'udp', server: srv });
  }
  for (const u of own.v6 || []) {
    for (const srv of u.servers) rows.push({ key: `v6|${srv}`, label: srv, sub: `подключение ${u.interface} (IPv6)`, kind: 'udp', server: srv });
  }
  return rows;
}

// совет по усилению DNS на самом Keenetic (дизайн-слово владельца
// 2026-10-18: НЕ советовать простой UDP «запасным» - на сетях с подменой
// он первым отравляется; простые серверы - только при уверенности в
// отсутствии подмены или под антиподменой вроде zator)
export function ownAdviceOf(
  ownV: KeeneticOwn,
  verdicts: Record<string, DnsProbeRow>,
  rows: KeeneticOwnRow[],
): { encrypted: number; udpCount: number; spoofSeen: boolean } | null {
  const encrypted = (ownV.doh || []).length + (ownV.dot || []).length;
  const udpCount = (ownV.udp || []).length + (ownV.v6 || []).length;
  const spoofSeen = Object.values(verdicts).some(
    (r) => r.verdict === 'spoof' && rows.find((e) => e.key && e.kind === 'udp'),
  );
  return { encrypted, udpCount, spoofSeen };
}

// шифрованные (DoH/DoT) - всегда в кадре; простые по подключениям -
// свёрнуты: лежачие туннели дают честные «недоступен» на каждую запись
// и захламляют список (слово владельца: не вводить в заблуждение)
export function ownEncryptedOf(rows: KeeneticOwnRow[]): KeeneticOwnRow[] {
  return rows.filter(
    (e) => e.kind === 'doh' || e.kind === 'dot' || e.key === 'empty-doh' || e.key === 'empty-dot',
  );
}

export function ownPlainOf(rows: KeeneticOwnRow[]): KeeneticOwnRow[] {
  return rows.filter((e) => e.kind === 'udp' || e.key === 'empty-udp');
}

export function ownPlainRealOf(rows: KeeneticOwnRow[]): KeeneticOwnRow[] {
  return rows.filter((e) => e.kind === 'udp');
}
