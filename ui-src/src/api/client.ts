// Единая fetch-обёртка (44-дока, Фаза 1): семантика legacy api()/mtApi() -
// JSON-тело и ответ, ошибки как {error} -> ApiError, 401 -> событие
// UNAUTHORIZED_EVENT и заморозка вызова (страницу перезагрузит оверлей
// логина, как в legacy), таймаут через AbortController.

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

export const UNAUTHORIZED_EVENT = 'mawg:unauthorized';

export interface ApiOptions {
  timeoutMs?: number
}

export async function api<T = unknown>(
  method: string,
  path: string,
  body?: unknown,
  opts: ApiOptions = {},
): Promise<T> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), opts.timeoutMs ?? 90_000);
  let resp: Response;
  try {
    resp = await fetch('/api/v1' + path, {
      method,
      headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
      body: body !== undefined ? JSON.stringify(body) : undefined,
      signal: ctrl.signal,
    });
  } catch (e) {
    const abort = e instanceof DOMException && e.name === 'AbortError';
    throw new ApiError(abort ? 'таймаут запроса' : 'панель недоступна', 0);
  } finally {
    clearTimeout(timer);
  }
  if (resp.status === 401 && !path.startsWith('/auth/')) {
    window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT, { detail: path }));
    // замораживаемся, как legacy: ответ придёт только после перезагрузки
    // страницы оверлеем логина
    await new Promise<never>(() => {});
  }
  if (!resp.ok) {
    let msg = 'HTTP ' + resp.status;
    try {
      const data = (await resp.json()) as { error?: unknown };
      if (data && typeof data.error === 'string' && data.error) msg = data.error;
    } catch {
      // тело не JSON - оставляем HTTP-код
    }
    throw new ApiError(msg, resp.status);
  }
  const text = await resp.text();
  if (!text) return undefined as T;
  return JSON.parse(text) as T;
}
