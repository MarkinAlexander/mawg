// Ожидание возврата панели после запуска обновления (порт хвоста легаси-
// обработчика #updBadge). Поллинг - raw fetch МИМО api()-клиента: 401 тут
// не «заморозка», а признак «панель уже перезапустилась, сессия сброшена»
// (легаси тоже ходит сырым fetch и считает 401 успехом ожидания).
import { reactive } from 'vue'

export interface UpdateWaitState {
  // wait - установка/перезапуск идёт (закрытие запрещено); failed - не
  // дождались, показываем текст и «Закрыть» (легаси finish(msg))
  phase: 'wait' | 'failed'
  text: string
  seconds: number
}

export interface WaitResp {
  status: number
  ok: boolean
  json(): Promise<{ version?: string }>
}

export interface UpdateWaitDeps {
  fetchFn?: (url: string) => Promise<WaitResp>
  onReload?: () => void
  tries?: number
}

const POLL_MS = 2000;
const START_DELAY_MS = 3000;
const TICK_MS = 1000;
const DEFAULT_TRIES = 150; // 150 * 2с после паузы 3с ~ 5 минут, как легаси

const TIMEOUT_TEXT =
  'Не дождались возврата панели за 5 минут. Обновление могло затянуться: ' +
  'обновите страницу вручную и проверьте версию в разделе Настройки.';

export function startUpdateWait(
  fromVersion: string,
  latest: string,
  deps: UpdateWaitDeps = {},
) {
  const state = reactive<UpdateWaitState>({
    phase: 'wait',
    text: `скачиваем и устанавливаем ${latest}; панель перезапустится, страница обновится сама`,
    seconds: 0,
  });
  const fetchFn =
    deps.fetchFn ??
    ((url: string) => fetch(url, { credentials: 'same-origin' }) as Promise<WaitResp>);
  const onReload = deps.onReload ?? (() => window.location.reload());
  const tries = deps.tries ?? DEFAULT_TRIES;

  let done = false;
  const timeouts: ReturnType<typeof setTimeout>[] = [];
  const tick = setInterval(() => {
    state.seconds++;
  }, TICK_MS);

  function finish(msg?: string): void {
    if (done) return;
    done = true;
    clearInterval(tick);
    if (!msg) {
      onReload();
      return;
    }
    state.phase = 'failed';
    state.text = msg;
  }

  function poll(n: number): void {
    if (done) return;
    fetchFn('/api/v1/status')
      .then((r) => {
        if (r.status === 401) return finish(); // панель уже перезапустилась
        if (!r.ok) throw 0;
        return r.json().then((j) => {
          if (j.version && j.version !== fromVersion) finish();
          else state.text = `устанавливаем ${latest} (панель ещё работает на ${fromVersion})...`;
        });
      })
      .catch(() => {
        if (!done) state.text = 'панель перезапускается, ждём возврата...';
      })
      .finally(() => {
        if (done) return;
        if (n <= 0) return finish(TIMEOUT_TEXT);
        timeouts.push(setTimeout(() => poll(n - 1), POLL_MS));
      });
  }
  timeouts.push(setTimeout(() => poll(tries), START_DELAY_MS));

  // размонтирование компонента не должно оставлять таймеры (легаси не
  // умел отменять - страница всё равно перезагружалась)
  function stop(): void {
    done = true;
    clearInterval(tick);
    timeouts.forEach(clearTimeout);
  }
  return { state, stop };
}
