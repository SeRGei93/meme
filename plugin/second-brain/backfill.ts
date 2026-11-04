import { createHash } from 'node:crypto';
import type { CaptureResult } from './capture.ts';
import type { StorageLike } from './cursor.ts';
import type { SessionInfoLike } from './types.ts';

export interface BackfillSessions {
  list(input: {
    directory: string;
    limit?: number;
    cursor?: string;
  }): Promise<{
    data: readonly SessionInfoLike[];
    cursor?: { next?: string | null };
  }>;
}

export interface BackfillCapture {
  capture(sessionID: string, options: { final: boolean }): Promise<CaptureResult>;
}

export interface BackfillDeps {
  readonly sessions: BackfillSessions;
  readonly capture: BackfillCapture;
  readonly storage: StorageLike;
  readonly directory: string;
  readonly pageSize?: number;
  readonly throttleMs?: number;
  readonly sleep?: (ms: number) => Promise<void>;
  readonly now?: () => Date;
  readonly onError?: (error: unknown) => void;
  readonly signal?: AbortSignal;
}

/** Progress marker kept in `ctx.storage`, keyed per project directory. */
export interface BackfillState {
  version: 1;
  directory: string;
  /** Page to resume from when the previous run was interrupted. */
  nextCursor?: string;
  sessions: number;
  written: number;
  failed: number;
  completedAt?: string;
}

export interface BackfillResult {
  readonly sessions: number;
  readonly written: number;
  readonly failed: number;
  readonly pages: number;
  readonly skipped: boolean;
}

const DEFAULT_PAGE_SIZE = 50;
/** Enough to keep writes gentle on the spool and on OpenCode while backfilling. */
const DEFAULT_THROTTLE_MS = 200;

/**
 * Imports sessions that existed before the plugin was installed.
 *
 * Pages through `session.list`, writes each session through the normal capture
 * path (so per-session cursors and fingerprints prevent duplicates), survives a
 * single broken session, and resumes from the last page after an abort. A
 * completed marker per directory makes subsequent launches a no-op. If any
 * session failed, the marker is left incomplete so the next launch retries.
 */
export async function runBackfill(deps: BackfillDeps): Promise<BackfillResult> {
  const pageSize = deps.pageSize ?? DEFAULT_PAGE_SIZE;
  const throttleMs = deps.throttleMs ?? DEFAULT_THROTTLE_MS;
  const sleep = deps.sleep ?? defaultSleep;
  const now = deps.now ?? (() => new Date());
  const onError = deps.onError ?? (() => {});
  const key = stateKey(deps.directory);

  const previous = readState(await deps.storage.get(key), deps.directory);
  if (previous?.completedAt) {
    return {
      sessions: previous.sessions,
      written: previous.written,
      failed: previous.failed,
      pages: 0,
      skipped: true,
    };
  }

  const state: BackfillState = previous ?? {
    version: 1,
    directory: deps.directory,
    sessions: 0,
    written: 0,
    failed: 0,
  };

  let cursor = state.nextCursor;
  let pages = 0;
  let writtenThisRun = 0;
  let failedThisRun = 0;
  let aborted = false;

  while (true) {
    if (deps.signal?.aborted) {
      aborted = true;
      break;
    }

    let page: Awaited<ReturnType<BackfillSessions['list']>>;
    try {
      page = await deps.sessions.list({ directory: deps.directory, limit: pageSize, cursor });
    } catch (error) {
      onError(error);
      await deps.storage.set(key, { ...state, failed: failedThisRun });
      return result(state, writtenThisRun, failedThisRun, pages, false);
    }
    pages += 1;

    for (const session of page.data) {
      if (deps.signal?.aborted) {
        aborted = true;
        break;
      }

      state.sessions += 1;
      try {
        const captured = await deps.capture.capture(session.id, { final: true });
        if (captured.written) {
          state.written += 1;
          writtenThisRun += 1;
        }
      } catch (error) {
        failedThisRun += 1;
        onError(error);
      }
      await sleep(throttleMs);
    }

    if (aborted) break;

    const next = page.cursor?.next ?? undefined;
    if (!next || next === cursor) {
      state.nextCursor = undefined;
      state.failed = failedThisRun;
      if (failedThisRun === 0) {
        state.completedAt = now().toISOString();
      }
      await deps.storage.set(key, state);
      return result(state, writtenThisRun, failedThisRun, pages, false);
    }

    cursor = next;
    state.nextCursor = cursor;
    state.failed = failedThisRun;
    await deps.storage.set(key, state);
  }

  state.failed = failedThisRun;
  await deps.storage.set(key, state);
  return result(state, writtenThisRun, failedThisRun, pages, false);
}

function result(
  state: BackfillState,
  written: number,
  failed: number,
  pages: number,
  skipped: boolean,
): BackfillResult {
  return { sessions: state.sessions, written, failed, pages, skipped };
}

function stateKey(directory: string): string {
  const hash = createHash('sha256').update(directory).digest('hex').slice(0, 16);
  return `backfill/v1/${hash}`;
}

function readState(value: unknown, directory: string): BackfillState | undefined {
  if (typeof value !== 'object' || value === null) return undefined;
  const state = value as Record<string, unknown>;
  if (state.version !== 1 || state.directory !== directory) return undefined;
  if (
    typeof state.sessions !== 'number' ||
    typeof state.written !== 'number' ||
    typeof state.failed !== 'number'
  ) {
    return undefined;
  }
  if (state.nextCursor !== undefined && typeof state.nextCursor !== 'string') return undefined;
  if (state.completedAt !== undefined && typeof state.completedAt !== 'string') return undefined;
  return state as unknown as BackfillState;
}

function defaultSleep(ms: number): Promise<void> {
  if (ms <= 0) return Promise.resolve();
  return new Promise((resolve) => setTimeout(resolve, ms));
}
