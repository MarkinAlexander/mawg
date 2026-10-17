// История браузера как «Назад» для модалок (слово владельца: с телефона -
// клавиша назад, с десктопа - тоже можно; незалоченная модалка закрывается
// так всегда). Каждый открытый диалог кладёт closer в стек + pushState со
// своим тегом {mawgDlg: id}; popstate закрывает верхний. Закрытие иными
// путями (Esc, крест, клик по пустому месту) само делает history.back(),
// ТОЛЬКО если запись диалога всё ещё верхняя (history.state сверяется по
// id): если поверх неё уже произошла роутер-навигация (переход по
// результату поиска - SearchDialog.go), back() отменил бы её (живая
// приёмка на VM), поэтому запись остаётся «мёртвым» тегом и проскакивается
// в onPopstate автоматическим back(). selfPops гасит попстейт от этого
// собственного back(). jsdom-прогон: держать history.pushState/back в
// стабах и сбрасывать dialogBackReset().
type Closer = () => void

interface Entry {
  id: number
  closer: Closer
}

const stack: Entry[] = []
const alive = new Map<number, boolean>()
let seq = 0
let selfPops = 0
let installed = false

function taggedId(): number | null {
  const st = window.history.state as { mawgDlg?: number } | null
  return st && typeof st.mawgDlg === 'number' ? st.mawgDlg : null
}

function onPopstate(): void {
  if (selfPops > 0) {
    selfPops--
    return
  }
  // мусорная запись закрытого диалога - проскакиваем, ничего не закрывая
  const tag = taggedId()
  if (tag !== null && !alive.get(tag)) {
    history.back()
    return
  }
  const top = stack.pop()
  if (top) {
    alive.delete(top.id)
    top.closer()
  }
}

export function dialogBackOpen(closer: Closer): void {
  if (!installed) {
    window.addEventListener('popstate', onPopstate)
    installed = true
  }
  const id = ++seq
  stack.push({ id, closer })
  alive.set(id, true)
  history.pushState({ mawgDlg: id }, '')
}

export function dialogBackClose(closer: Closer, byPop: boolean): void {
  const i = stack.findIndex((e) => e.closer === closer)
  if (i < 0) return
  const [entry] = stack.splice(i, 1)
  alive.set(entry.id, false)
  // запись диалога ещё верхняя - снимаем её; если поверх уже была
  // навигация - не трогаем (иначе отменили бы её, см. шапку)
  if (!byPop && taggedId() === entry.id) {
    selfPops++
    history.back()
  }
}

// переход поверх диалога (SearchDialog.go): ждём, пока закрывающийся
// диалог снимет свою запись из истории, чтобы router.push не откатился
export function dialogBackWaitClean(timeoutMs = 600): Promise<void> {
  const t0 = Date.now()
  return new Promise((resolve) => {
    const tick = () => {
      if (taggedId() === null || Date.now() - t0 > timeoutMs) return resolve()
      setTimeout(tick, 30)
    }
    tick()
  })
}

// тестам: снять слушатель и обнулить состояние между тестами
export function dialogBackReset(): void {
  if (installed) {
    window.removeEventListener('popstate', onPopstate)
    installed = false
  }
  stack.length = 0
  alive.clear()
  selfPops = 0
}
