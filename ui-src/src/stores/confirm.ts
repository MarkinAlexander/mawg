// Промис-модалка подтверждения (замена legacy askModal()): useConfirm()
// возвращает state для рендера и confirm() - промис с true/false.
// Повторный confirm до ответа отменяет предыдущий как fallback.
// confirmChoice() - вариант с третьей кнопкой (altLabel, 97-док:
// «удалить пул и устройства» / «оставить напрямую» / отмена).
import { reactive } from 'vue'

export interface ConfirmState {
  open: boolean
  title: string
  text: string
  okLabel: string
  altLabel: string // пусто - третьей кнопки нет
  info: boolean // info-модалка: без «Отмены» (только «ОК»)
}

const state = reactive<ConfirmState>({
  open: false,
  title: '',
  text: '',
  okLabel: 'ОК',
  altLabel: '',
  info: false,
});

type ConfirmResult = boolean | 'alt';

let resolve: ((v: ConfirmResult | null) => void) | null = null;
// чем закрывать модалку, сорванную новым вопросом (confirm - false,
// confirmChoice - null)
let fallback: ConfirmResult | null = false;

export function useConfirm() {
  function open_(opts: { title: string; text: string; okLabel?: string; altLabel?: string; info?: boolean }): void {
    if (resolve) resolve(fallback);
    state.title = opts.title;
    state.text = opts.text;
    state.okLabel = opts.okLabel || 'ОК';
    state.altLabel = opts.altLabel || '';
    state.info = !!opts.info;
    state.open = true;
  }
  function confirm(opts: { title: string; text: string; okLabel?: string; info?: boolean }): Promise<boolean> {
    open_(opts);
    fallback = false;
    return new Promise<boolean>((r) => {
      resolve = (v) => r(v === true);
    });
  }
  // третья кнопка: 'ok' - главная (okLabel), 'alt' - altLabel, null - отмена
  function confirmChoice(opts: { title: string; text: string; okLabel?: string; altLabel?: string }): Promise<'ok' | 'alt' | null> {
    open_(opts);
    fallback = null;
    return new Promise<'ok' | 'alt' | null>((r) => {
      resolve = (v) => r(v === true ? 'ok' : v === false ? null : v);
    });
  }
  function settle(v: ConfirmResult) {
    state.open = false;
    const r = resolve;
    resolve = null;
    fallback = false;
    r?.(v);
  }
  return { state, confirm, confirmChoice, ok: () => settle(true), alt: () => settle('alt'), cancel: () => settle(false) };
}
