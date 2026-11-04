import { readFile as defaultReadFile } from 'node:fs/promises';
import { join } from 'node:path';
import type { PluginConfig } from './config.ts';

export type FetchLike = (input: string | URL | Request, init?: RequestInit) => Promise<Response>;

export interface RememberDeps {
  readonly config: PluginConfig;
  /** Stable project key, or a resolver using the same rules as capture. */
  readonly projectKey: string | (() => Promise<string>);
  readonly readFile?: (path: string) => Promise<string>;
  readonly fetch?: FetchLike;
  readonly timeoutMs?: number;
}

export interface RememberCommandDeps extends RememberDeps {
  /** Injects a visible message into the OpenCode session. */
  readonly say?: (input: { sessionID: string; text: string }) => Promise<unknown>;
}

export type RememberOutcome =
  | {
      readonly ok: true;
      readonly recordID: number;
      readonly status: 'indexed' | 'queued';
      readonly replayed: boolean;
    }
  | { readonly ok: false; readonly message: string };

interface MemoryResponse {
  record_id: number;
  job_id: number | null;
  status: 'indexed' | 'queued';
  replayed: boolean;
}

interface ErrorEnvelope {
  error?: { code?: string; message?: string; request_id?: string };
}

const DEFAULT_TIMEOUT_MS = 5_000;
const MAX_TEXT_CHARS = 2_000_000;
/** Opaque bearer token from CONTRACTS.md §4.1: `sb_<kind>_<43 base64url>`. */
const TOKEN_PATTERN = /^sb_[a-z]+_[A-Za-z0-9_-]{43}$/;

/**
 * Sends one `/remember` text to `POST /api/v1/memories`.
 *
 * Never throws: the command must stay usable with an unreachable server, so
 * every failure becomes a user-facing message. `occurred_at` is deliberately
 * omitted: the server includes it in the content hash, so stamping every call
 * with "now" would make two identical memories look different and break the
 * content-hash idempotency. `source` is implied by the bearer token.
 */
export async function remember(text: string, deps: RememberDeps): Promise<RememberOutcome> {
  const rawText = text.trim();
  if (rawText === '') {
    return { ok: false, message: 'Укажите текст: `/remember <что запомнить>`' };
  }
  if (rawText.length > MAX_TEXT_CHARS) {
    return { ok: false, message: `Текст больше ${MAX_TEXT_CHARS} символов` };
  }

  const serverURL = deps.config.serverURL?.trim();
  if (!serverURL) {
    return {
      ok: false,
      message: 'Сервер не настроен. Запустите SecondBrain.app и пройдите onboarding',
    };
  }

  let base: URL;
  try {
    base = new URL(serverURL);
  } catch {
    return { ok: false, message: 'Некорректный URL сервера в config.json' };
  }
  if (base.search !== '' || base.hash !== '') {
    return { ok: false, message: 'URL сервера не должен содержать query или fragment' };
  }
  if (base.protocol !== 'https:' && !isLoopback(base.hostname)) {
    return { ok: false, message: 'Сервер должен использовать https (http разрешён только для localhost)' };
  }
  const endpoint = new URL('api/v1/memories', base.href.endsWith('/') ? base.href : `${base.href}/`);

  const readFile = deps.readFile ?? ((path: string) => defaultReadFile(path, 'utf8'));
  let token: string;
  try {
    token = (await readFile(join(deps.config.configDir, 'mcp_token'))).trim();
  } catch {
    token = '';
  }
  if (token === '') {
    return {
      ok: false,
      message: 'Нет токена OpenCode. Запустите SecondBrain.app → Settings → «Повернуть токены…»',
    };
  }
  if (!TOKEN_PATTERN.test(token)) {
    return {
      ok: false,
      message: 'mcp_token повреждён. Запустите SecondBrain.app → Settings → «Повернуть токены…»',
    };
  }

  let projectKey: string;
  try {
    projectKey = typeof deps.projectKey === 'function' ? await deps.projectKey() : deps.projectKey;
  } catch {
    return { ok: false, message: 'Не удалось определить проект для записи' };
  }

  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), deps.timeoutMs ?? DEFAULT_TIMEOUT_MS);
  let response: Response;
  let body: unknown;
  try {
    response = await (deps.fetch ?? fetch)(endpoint, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${token}`,
        'Content-Type': 'application/json',
        Accept: 'application/json',
      },
      body: JSON.stringify({ raw_text: rawText, project_key: projectKey }),
      signal: controller.signal,
    });
    // Keep the timer alive while the body is read: a proxy can send headers and
    // then stall the body forever.
    body = await readJSON(response);
  } catch (error) {
    if (controller.signal.aborted) {
      return { ok: false, message: 'Сервер недоступен: истекло время ожидания' };
    }
    const reason = error instanceof Error ? error.message : String(error);
    return { ok: false, message: `Сервер недоступен: ${reason}` };
  } finally {
    clearTimeout(timeout);
  }

  if (controller.signal.aborted) {
    return { ok: false, message: 'Сервер недоступен: истекло время ожидания' };
  }

  if (!response.ok) {
    if (response.status === 429) {
      const retryAfter = response.headers.get('Retry-After');
      return {
        ok: false,
        message: retryAfter
          ? `Слишком много запросов, повтор через ${retryAfter} с`
          : 'Слишком много запросов, попробуйте позже',
      };
    }
    if (response.status === 401 || response.status === 403) {
      return {
        ok: false,
        message: `Токен отклонён сервером (${response.status}). Запустите onboarding и повторите`,
      };
    }
    const details = describeError(body);
    return {
      ok: false,
      message: `Сервер ответил ${response.status}${details ? `: ${details}` : ''}`,
    };
  }

  const parsed = parseMemoryResponse(body);
  if (!parsed) {
    return { ok: false, message: 'Сервер вернул ответ в неизвестном формате' };
  }
  return {
    ok: true,
    recordID: parsed.record_id,
    status: parsed.status,
    replayed: parsed.replayed,
  };
}

/**
 * OpenCode command entrypoint. Success and failure are both shown as a
 * synthetic session message; logging is the fallback when even that fails.
 */
export async function runRememberCommand(
  invocation: { sessionID: string; prompt: { text: string } },
  deps: RememberCommandDeps,
): Promise<void> {
  let outcome: RememberOutcome;
  try {
    outcome = await remember(stripCommand(invocation.prompt.text), deps);
  } catch (error) {
    const reason = error instanceof Error ? error.message : String(error);
    outcome = { ok: false, message: reason };
  }

  const message = outcome.ok
    ? successMessage(outcome)
    : `Не удалось запомнить: ${outcome.message}`;

  if (!deps.say) {
    // No message channel (old adapter): surface the failure to OpenCode and
    // keep a success at least in the log.
    if (!outcome.ok) throw new Error(message);
    console.info(`[second-brain] ${message}`);
    return;
  }
  try {
    await deps.say({ sessionID: invocation.sessionID, text: message });
  } catch (error) {
    const reason = error instanceof Error ? error.message : String(error);
    console.error(`[second-brain] remember: ${message} (${reason})`);
  }
}

/** Some OpenCode versions pass the full command text to `execute`. */
function stripCommand(text: string): string {
  return text.replace(/^\s*\/remember\b\s*/i, '');
}

function successMessage(outcome: Extract<RememberOutcome, { ok: true }>): string {
  if (outcome.replayed) {
    return `Уже в памяти: запись #${outcome.recordID}`;
  }
  if (outcome.status === 'queued') {
    return `Запомнено: запись #${outcome.recordID}, карточка сгенерируется позже`;
  }
  return `Запомнено: запись #${outcome.recordID}`;
}

async function readJSON(response: Response): Promise<unknown> {
  // `text()` rejects when the fetch signal aborts; only JSON parse errors are
  // swallowed here so the timeout above stays visible.
  const text = await response.text();
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

function describeError(body: unknown): string {
  if (typeof body !== 'object' || body === null) return '';
  const envelope = body as ErrorEnvelope;
  const message = envelope.error?.message;
  const code = envelope.error?.code;
  const requestID = envelope.error?.request_id;
  const parts: string[] = [];
  if (message) parts.push(message);
  if (code) parts.push(`(${code})`);
  if (requestID) parts.push(`request_id: ${requestID}`);
  return parts.join(' ');
}

function parseMemoryResponse(body: unknown): MemoryResponse | undefined {
  if (typeof body !== 'object' || body === null) return undefined;
  const value = body as Record<string, unknown>;
  if (
    typeof value.record_id !== 'number' ||
    !Number.isInteger(value.record_id) ||
    value.record_id <= 0 ||
    (value.job_id !== null &&
      (typeof value.job_id !== 'number' || !Number.isInteger(value.job_id))) ||
    (value.status !== 'indexed' && value.status !== 'queued') ||
    typeof value.replayed !== 'boolean'
  ) {
    return undefined;
  }
  return value as unknown as MemoryResponse;
}

function isLoopback(host: string): boolean {
  return ['localhost', '127.0.0.1', '::1', '[::1]'].includes(host.toLowerCase());
}
