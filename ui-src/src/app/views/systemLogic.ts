// Чистые функции вкладки «Система»: журнал событий (лейблы kinds,
// фильтры, хвост 80), таблица зависимостей, состояния ядер и движка
// sing-box. Refs/опросы/мутации - во вьюхе и секциях-компонентах
// (SystemView/SystemCores/SystemDiag/SystemJournal), сюда данные
// приходят параметрами.
import type { UiSelectOption, UiTableColumn } from '../../components/ui'
import type { AppEvent, CoreInfo, EngineInfo } from '../../api/types'

// человеческие лейблы типов событий (ключи kind - API, не трогаем)
export const KIND_RU: Record<string, string> = {
  rotate: 'ротация', applied: 'применено', fallback: 'фоллбек',
  magitrickle: 'MagiTrickle', install: 'установка', policy: 'политика',
  sources: 'источник', probe: 'проверка', subupdate: 'подписка',
  restore: 'восстановление', 'smart-cascade': 'умный каскад',
  cascade: 'каскад', slots: 'слоты', warp: 'WARP',
};
export const kindRu = (k: string) => KIND_RU[k] || k;

// дропдаун типов журнала: собран из данных, пустой value = «все события»
export function evKindOptionsOf(list: AppEvent[]): UiSelectOption[] {
  return [
    { value: '', label: 'все события' },
    ...[...new Set(list.map((e) => e.kind))].sort().map((k) => ({ value: k, label: kindRu(k) })),
  ];
}

// фильтр - по всему списку, хвост 80 - уже отфильтрованного (легаси
// slice(-80).reverse(): последняя запись сверху)
export function evFilteredOf(list: AppEvent[], kind: string, query: string): AppEvent[] {
  const q = query.trim().toLowerCase();
  return list
    .filter(
      (e) =>
        (!kind || e.kind === kind) &&
        (!q || e.message.toLowerCase().includes(q) || (e.pool || '').toLowerCase().includes(q)),
    )
    .slice(-80)
    .reverse();
}

export function fmtTime(iso: string): string {
  return iso ? new Date(iso).toLocaleTimeString('ru-RU') : '';
}

// источник сборки ядра sing-box (Item.Source) - бейдж рядом с версией:
// владелец путается, чьё ядро стоит (92-док)
const sourceLabels: Record<string, string> = {
  extended: 'extended', lx: 'lx', 'lx-own': 'lx-own', upstream: 'апстрим',
};
export const sourceLabel = (s: string): string => sourceLabels[s] || s;

export const SYSTEM_COLUMNS: UiTableColumn[] = [
  { key: 'title', label: 'компонент' },
  { key: 'version', label: 'версия' },
  { key: 'state', label: 'состояние' },
  { key: 'action', label: '' },
];

export const coreStateBadge = (c: CoreInfo): { text: string; variant: 'ok' | 'err' | 'warn' } => {
  if (c.disabled) return { text: 'выключен', variant: 'err' };
  return c.running ? { text: 'работает', variant: 'ok' } : { text: 'остановлен', variant: 'warn' };
};

export function engineInfoOf(eng: EngineInfo | undefined): string {
  if (!eng || !eng.available) return 'движок не найден';
  // честные состояния (слово владельца 2026-10-17): «не запущено» -
  // процесса нет; «запущено, но не отвечает» - API молчит
  const state = !eng.running
    ? 'ядро не запущено'
    : eng.api === false
      ? 'ядро запущено, но не отвечает'
      : 'ядро работает';
  const parts = [state];
  if (eng.mode === 'shared' && eng.fragment) parts.push(eng.fragment);
  return parts.join(' | ');
}

// кэш общего ядра пишется на флешку (не /tmp) - предупреждение об износе
export const sbCacheWarnOf = (eng: EngineInfo | undefined): boolean =>
  !!eng && eng.mode === 'shared' && !!eng.cacheFile && !eng.cacheFile.startsWith('/tmp');

export const SB_MODE_OPTIONS: UiSelectOption[] = [
  { value: 'own', label: 'свой экземпляр' },
  { value: 'shared', label: 'общее ядро (-C merge)' },
];

export const SB_CACHE_TEXT = 'Кэш ядра пишется на флешку - постоянный износ ~170МБ/сутки. Выключить совсем: ничего не пишется, выборы сбрасываются при рестарте ядра. В /tmp: не пишется на флешку, выборы живут до ребута роутера.';

// текст диалога хода установки - по устанавливаемому компоненту
export function busyTextOf(busyId: string): string {
  if (busyId === 'magitrickle')
    return 'Качаем релиз форка из GitHub и обновляем штатным менеджером пакетов; конфиг и группы сохраняются, служба MagiTrickle перезапустится. Это занимает до пары минут.';
  if (busyId === 'mihomo')
    return 'Качаем релиз mihomo (Clash.Meta) из GitHub, проверяем сумму и тест-запуск. Распакованное ядро ~54 МБ - это до пары минут.';
  return 'Идёт установка. Это может занять до пары минут - не закрывайте панель.';
}

// размер релизного ассета в мегабайтах для подписи ссылки
export const dlMb = (b?: number) => (b ? (b / 1048576).toFixed(0) + ' МБ' : '');

export const fmtStarted = (iso?: string) => (iso ? new Date(iso).toLocaleTimeString('ru-RU') : '');
