// Приватный режим (62-док): глобальный флаг «размыть IP/ключи/подписки»
// для скриншотов и записи экрана. Хранится в localStorage (mawg-privacy,
// '1'/'0', по умолчанию 0), переживает перезагрузку страницы. Класс
// privacy-on на body и проявление отдельных полей (secret-open) -
// на NextLayout (шапка видна на всех вкладках).
import { reactive } from 'vue'

export const PRIVACY_LS_KEY = 'mawg-privacy'

const state = reactive<{ on: boolean }>({ on: false })
let restored = false

export function usePrivacy() {
  // localStorage читаем один раз на сессию: дальше источник истины - стор
  if (!restored) {
    restored = true;
    try {
      state.on = localStorage.getItem(PRIVACY_LS_KEY) === '1';
    } catch {
      // нет localStorage - режим просто не переживает перезагрузку
    }
  }
  function set(on: boolean): void {
    state.on = on;
    try {
      localStorage.setItem(PRIVACY_LS_KEY, on ? '1' : '0');
    } catch {
      // см. выше
    }
  }
  function toggle(): void {
    set(!state.on);
  }
  return { state, set, toggle };
}
