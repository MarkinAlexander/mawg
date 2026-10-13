// Единая fetch-обёртка (44-дока, Фаза 1): семантика legacy api()/mtApi() -
// JSON-тело и ответ, ошибки как {error} -> ApiError, 401 -> событие
// UNAUTHORIZED_EVENT и заморозка вызова (страницу перезагрузит оверлей
// логина, как в legacy), таймаут через AbortController.
import { reactive } from 'vue'

// Индикатор занятости для шапки (legacy #topbar): счётчик активных
// api()-вызовов. Замороженный на 401 вызов счётчик не отпускает - как
// в legacy, где topbarEnd после заморозки не вызывался.
export const inflight = reactive({ n: 0 })

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

async function apiCall<T = unknown>(
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

export async function api<T = unknown>(
  method: string,
  path: string,
  body?: unknown,
  opts: ApiOptions = {},
): Promise<T> {
  inflight.n++;
  try {
    return await apiCall<T>(method, path, body, opts);
  } finally {
    inflight.n--;
  }
}

// сырой текст (легаси apiText: конфиг Free и т.п.) - без JSON-парсинга,
// с теми же 401-заморозкой и topbar-счётчиком
export async function apiText(path: string): Promise<string> {
  inflight.n++;
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), 90_000);
  try {
    let resp: Response;
    try {
      resp = await fetch('/api/v1' + path, { signal: ctrl.signal });
    } catch (e) {
      const abort = e instanceof DOMException && e.name === 'AbortError';
      throw new ApiError(abort ? 'таймаут запроса' : 'панель недоступна', 0);
    } finally {
      clearTimeout(timer);
    }
    if (resp.status === 401 && !path.startsWith('/auth/')) {
      window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT, { detail: path }));
      await new Promise<never>(() => {});
    }
    if (!resp.ok) throw new ApiError('HTTP ' + resp.status, resp.status);
    return await resp.text();
  } finally {
    inflight.n--;
  }
}

// upload файла(-ов) multipart/form-data (легаси api(..., isForm)): та же
// семантика, но тело - FormData без JSON-заголовка
export async function apiForm<T = unknown>(
  method: string,
  path: string,
  form: FormData,
  opts: ApiOptions = {},
): Promise<T> {
  inflight.n++;
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), opts.timeoutMs ?? 90_000);
  try {
    let resp: Response;
    try {
      resp = await fetch('/api/v1' + path, { method, body: form, signal: ctrl.signal });
    } catch (e) {
      const abort = e instanceof DOMException && e.name === 'AbortError';
      throw new ApiError(abort ? 'таймаут запроса' : 'панель недоступна', 0);
    } finally {
      clearTimeout(timer);
    }
    if (resp.status === 401 && !path.startsWith('/auth/')) {
      window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT, { detail: path }));
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
  } finally {
    inflight.n--;
  }
}
