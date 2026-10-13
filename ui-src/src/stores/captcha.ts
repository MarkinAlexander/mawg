// Промис-модалка капчи Amnezia Free (легаси dlgCaptcha/openCaptcha 581-590,
// capSend/capRefresh 620-668): useCaptcha().ask(cap, refetch) ждёт решение,
// отмену или перезапрос картинки. Один на приложение - капча может прийти
// и при создании пула, и при перевыпуске из настроек.
import { reactive } from 'vue'

export interface CaptchaImg {
  id: string
  image: string
  hint?: string
}

const state = reactive({
  open: false,
  image: '',
  hint: '',
});

let resolve: ((v: { id: string; solution: string } | null) => void) | null = null;
let current: CaptchaImg | null = null;
let refetchFn: (() => Promise<CaptchaImg>) | null = null;

export function useCaptcha() {
  function ask(cap: CaptchaImg, refetch?: () => Promise<CaptchaImg>): Promise<{ id: string; solution: string } | null> {
    if (resolve) resolve(null); // повторный запрос отменяет предыдущий
    current = cap;
    refetchFn = refetch || null;
    state.image = cap.image;
    state.hint = cap.hint || 'Введите цифры с картинки, чтобы продолжить получение бесплатного конфига.';
    state.open = true;
    return new Promise((r) => {
      resolve = r;
    });
  }
  // легаси capRefresh: перезапросить картинку у gateway (id сменится)
  async function refresh(): Promise<void> {
    if (!refetchFn) return;
    try {
      const fresh = await refetchFn();
      current = fresh;
      state.image = fresh.image;
      state.hint = fresh.hint || state.hint;
    } catch {
      // сеть - картинка остаётся старой
    }
  }
  function answer(solution: string): void {
    state.open = false;
    const r = resolve;
    resolve = null;
    r?.({ id: current?.id || '', solution });
  }
  function cancel(): void {
    state.open = false;
    const r = resolve;
    resolve = null;
    r?.(null);
  }
  return { state, ask, refresh, answer, cancel };
}
