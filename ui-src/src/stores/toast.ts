// Очередь тостов (замена legacy toast()): максимум 5 на экране, каждый
// живёт 6 секунд. Синглтон-стор: состояние модульное, рендер - дело
// будущего UI-кита.
import { reactive } from 'vue'

export interface Toast {
  id: number
  text: string
  err: boolean
}

const VISIBLE_MS = 6000
const MAX_VISIBLE = 5

const state = reactive<{ list: Toast[] }>({ list: [] })
const timers = new Map<number, ReturnType<typeof setTimeout>>()
let seq = 0

export function dismissToast(id: number) {
  const i = state.list.findIndex((t) => t.id === id);
  if (i >= 0) state.list.splice(i, 1);
  const t = timers.get(id);
  if (t) {
    clearTimeout(t);
    timers.delete(id);
  }
}

export function useToast() {
  function push(text: string, err = false) {
    const id = ++seq;
    state.list.push({ id, text, err });
    if (state.list.length > MAX_VISIBLE) {
      dismissToast(state.list[0].id);
    }
    timers.set(id, setTimeout(() => dismissToast(id), VISIBLE_MS));
  }
  return { toasts: state, push, dismiss: dismissToast };
}
