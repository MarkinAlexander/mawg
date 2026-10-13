// Ожидание возврата панели после POST /system/restart (порт легаси
// waitForPanel, panel.js:2746): пауза, потом raw fetch /status каждые 2с
// до 30 попыток. Успех = ok ИЛИ 401 (панель перезапустилась, сессия
// сброшена - это тоже «вернулась»). Переезд на новый origin опрашивается
// no-cors: кросс-портовый fetch читает только факт ответа, connection
// refused роняет промис - этого достаточно.
import { reactive } from 'vue'

export interface RestartResp {
  ok: boolean
  status: number
}

export interface RestartWaitDeps {
  fetchFn?: (url: string, init?: { mode?: RequestMode; credentials?: RequestCredentials }) => Promise<RestartResp>
  onDone: (ok: boolean) => void
  tries?: number
  delayMs?: number
  pollMs?: number
}

const POLL_MS = 2000;
const START_DELAY_MS = 4000;
const DEFAULT_TRIES = 30;

export function startRestartWait(newOrigin: string | null, deps: RestartWaitDeps) {
  const state = reactive({ running: true });
  const fetchFn =
    deps.fetchFn ??
    ((url: string, init?: { mode?: RequestMode; credentials?: RequestCredentials }) =>
      fetch(url, init) as Promise<RestartResp>);
  const tries = deps.tries ?? DEFAULT_TRIES;
  const delayMs = deps.delayMs ?? START_DELAY_MS;
  const pollMs = deps.pollMs ?? POLL_MS;

  let done = false;
  let n = tries;
  const timeouts: ReturnType<typeof setTimeout>[] = [];

  function finish(ok: boolean): void {
    if (done) return;
    done = true;
    state.running = false;
    deps.onDone(ok);
  }

  function poll(): void {
    if (done) return;
    // переезд: no-cors ответ непрозрачен, любое разрешение = «отвечает»;
    // тот же origin: ok или 401 (сессия сброшена рестартом)
    const cross = !!newOrigin && newOrigin !== window.location.origin;
    const opts: { mode?: RequestMode; credentials?: RequestCredentials } = cross
      ? { mode: 'no-cors' }
      : { credentials: 'same-origin' };
    fetchFn((newOrigin || '') + '/api/v1/status', opts)
      .then((r) => {
        if (cross || r.ok || r.status === 401) return finish(true);
        throw 0; // не то - ждём дальше
      })
      .catch(() => {
        if (done) return;
        if (n <= 0) return finish(false);
        n--;
        timeouts.push(setTimeout(poll, pollMs));
      });
  }
  timeouts.push(setTimeout(poll, delayMs));

  function stop(): void {
    done = true;
    state.running = false;
    timeouts.forEach(clearTimeout);
  }
  return { state, stop };
}
