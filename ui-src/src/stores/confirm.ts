// Промис-модалка подтверждения (замена legacy askModal()): useConfirm()
// возвращает state для рендера и confirm() - промис с true/false.
// Повторный confirm до ответа отменяет предыдущий как false.
import { reactive } from 'vue'

export interface ConfirmState {
  open: boolean
  title: string
  text: string
  okLabel: string
  info: boolean // info-модалка: без «Отмены» (только «ОК»)
}

const state = reactive<ConfirmState>({
  open: false,
  title: '',
  text: '',
  okLabel: 'ОК',
  info: false,
});

let resolve: ((v: boolean) => void) | null = null;

export function useConfirm() {
  function confirm(opts: { title: string; text: string; okLabel?: string; info?: boolean }): Promise<boolean> {
    if (resolve) resolve(false);
    state.title = opts.title;
    state.text = opts.text;
    state.okLabel = opts.okLabel || 'ОК';
    state.info = !!opts.info;
    state.open = true;
    return new Promise<boolean>((r) => {
      resolve = r;
    });
  }
  function settle(v: boolean) {
    state.open = false;
    const r = resolve;
    resolve = null;
    r?.(v);
  }
  return { state, confirm, ok: () => settle(true), cancel: () => settle(false) };
}
