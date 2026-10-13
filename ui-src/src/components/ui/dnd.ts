// Чистая логика DnD-перестановки строк (паритет legacy attachDnD из
// panel.js): грип активирует draggable, половина высоты цели выбирает
// «до/после», индексы считаются с учётом удаления перетаскиваемого.

/** Перестановка с зажимом индексов; возвращает НОВЫЙ массив. */
export function reorder<T>(list: T[], from: number, to: number): T[] {
  const n = list.length
  const f = Math.min(Math.max(from, 0), n - 1)
  const t = Math.min(Math.max(to, 0), n - 1)
  if (f === t) return list.slice()
  const copy = list.slice()
  const [item] = copy.splice(f, 1)
  copy.splice(t, 0, item)
  return copy
}

/** Целевой индекс после удаления перетаскиваемого: «до цели» при ходе
 * вниз съедает одну позицию, «после цели» при ходе вверх добавляет. */
export function dropIndex(from: number, target: number, before: boolean): number {
  if (before) return target > from ? target - 1 : target
  return target > from ? target : target + 1
}

/** Курсор над верхней половиной цели? (legacy drop-before/drop-after) */
export function isBeforeHalf(rect: { top: number; height: number }, clientY: number): boolean {
  return clientY < rect.top + rect.height / 2
}
