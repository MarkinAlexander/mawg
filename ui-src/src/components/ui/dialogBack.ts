// История браузера как «Назад» для модалок (слово владельца: с телефона -
// клавиша назад, с десктопа - тоже можно; незалоченная модалка закрывается
// так всегда). Каждый открытый диалог кладёт closer в стек + pushState со
// своим тегом; popstate закрывает верхний. Закрытие иными путями (Esc,
// крест, клик по пустому месту, внешняя смена v-model) само делает
// history.back(), а попстейт от этого своего back() игнорируется счётчиком
// selfPops - чтобы не закрыть соседний диалог. jsdom-прогон: держать
// history.pushState/back в стабах и сбрасывать dialogBackReset().
type Closer = () => void

const stack: Closer[] = []
let selfPops = 0
let installed = false

function onPopstate(): void {
  if (selfPops > 0) {
    selfPops--
    return
  }
  stack.pop()?.()
}

export function dialogBackOpen(closer: Closer): void {
  if (!installed) {
    window.addEventListener('popstate', onPopstate)
    installed = true
  }
  stack.push(closer)
  history.pushState({ mawgDialog: true }, '')
}

export function dialogBackClose(closer: Closer, byPop: boolean): void {
  const i = stack.indexOf(closer)
  if (i < 0) return
  stack.splice(i, 1)
  if (!byPop) {
    selfPops++
    history.back()
  }
}

// тестам: снять слушатель и обнулить состояние между тестами
export function dialogBackReset(): void {
  if (installed) {
    window.removeEventListener('popstate', onPopstate)
    installed = false
  }
  stack.length = 0
  selfPops = 0
}
