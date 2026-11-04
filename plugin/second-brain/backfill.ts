import { createHash } from 'node:crypto';
import type { CaptureResult } from './capture.ts';
import type { StorageLike } from './cursor.ts';
import type { SessionInfoLike } from './types.ts';

export interface BackfillSessions {
  list(input: {
    directory: string;
    limit?: number;
    cursor?: string;
    parentID?: string | null;
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
  /** A session silent for this long is imported as final, like the server finalizer. */
  readonly finalAfterMs?: number;
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
  /** Sessions that failed and are retried on the next launch. */
  pending?: string[];
  attempts?: Record<string, number>;
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
/** Matches the server's 30-minute idle window. */
const DEFAULT_FINAL_AFTER_MS = 30 * 60_000;
/** A permanently broken session stops being retried after this many launches. */
const MAX_ATTEMPTS = 3;

/**
 * Imports sessions that existed before the plugin was installed.
 *
 * Pages through `session.list`, writes each session through the normal capture
 * path (so per-session cursors and fingerprints prevent duplicates), survives a
 * single broken session, and resumes from the last page after an abort. A
 * completed marker per directory makes subsequent launches a no-op. Failed
 * sessions are listed but only re-captured on later launches, and are dropped
 * after a few attempts so one broken session cannot block completion forever.
 */
export async function runBackfill(deps: BackfillDeps): Promise<BackfillResult> {
  const pageSize = deps.pageSize ?? DEFAULT_PAGE_SIZE;
  const throttleMs = deps.throttleMs ?? DEFAULT_THROTTLE_MS;
  const finalAfterMs = deps.finalAfterMs ?? DEFAULT_FINAL_AFTER_MS;
  const sleep = deps.sleep ?? defaultSleep;
  const now = deps.now ?? (() => new Date());
  const onError = deps.onError ?? (() => {});
  const key = stateKey(deps.directory);
  const startedAt = now().getTime();

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
  const pending = new Set(previous?.pending ?? []);
  const attempts = new Map(Object.entries(previous?.attempts ?? {}));
  // When a previous run left failures behind, keep walking the list (it is
  // cheap) but capture only those sessions.
  const retrying = pending.size > 0;

  let cursor = state.nextCursor;
  // The "vanished from list" check is only valid after a full walk from the
  // beginning; on a resumed walk pending ids may live on pages already skipped.
  const startedFromBeginning = cursor === undefined;
  let restarted = false;
  let pages = 0;
  let sessions = 0;
  let written = 0;
  let failed = 0;
  let aborted = false;
  const seen = new Set<string>();

  const persist = async () => {
    state.sessions = sessions;
    state.written = written;
    state.failed = failed;
    state.pending = pending.size > 0 ? [...pending].sort() : undefined;
    state.attempts = attempts.size > 0 ? Object.fromEntries(attempts) : undefined;
    await deps.storage.set(key, state);
  };

  const finish = async (completed: boolean): Promise<BackfillResult> => {
    state.nextCursor = undefined;
    if (completed && pending.size === 0) {
      state.completedAt = now().toISOString();
      state.pending = undefined;
      state.attempts = undefined;
      pending.clear();
      attempts.clear();
    }
    await persist();
    return { sessions, written, failed, pages, skipped: false };
  };

  while (true) {
    if (deps.signal?.aborted) {
      aborted = true;
      break;
    }

    let page: Awaited<ReturnType<BackfillSessions['list']>>;
    try {
      page = await deps.sessions.list({
        directory: deps.directory,
        limit: pageSize,
        cursor,
        parentID: null,
      });
    } catch (error) {
      if (cursor !== undefined && !restarted) {
        // The server may have pruned the cursor's anchor session or upgraded
        // its cursor format. Restart once from the beginning in the same run.
        restarted = true;
        cursor = undefined;
        state.nextCursor = undefined;
        onError(error);
        continue;
      }
      onError(error);
      await persist();
      return { sessions, written, failed, pages, skipped: false };
    }
    pages += 1;

    const next = page.cursor?.next ?? undefined;
    const rows = page.data;
    for (let index = 0; index < rows.length; index += 1) {
      if (deps.signal?.aborted) {
        aborted = true;
        break;
      }
      const session = rows[index]!;
      seen.add(session.id);
      if (retrying && !pending.has(session.id)) continue;

      sessions += 1;
      const isFinal = startedAt - session.time.updated >= finalAfterMs;
      try {
        const captured = await deps.capture.capture(session.id, { final: isFinal });
        if (captured.written) written += 1;
        pending.delete(session.id);
        attempts.delete(session.id);
      } catch (error) {
        failed += 1;
        const count = (attempts.get(session.id) ?? 0) + 1;
        attempts.set(session.id, count);
        if (count >= MAX_ATTEMPTS) {
          pending.delete(session.id);
        } else {
          pending.add(session.id);
        }
        onError(
          new Error(`backfill: session ${session.id} failed`, { cause: error }),
        );
      }

      const moreInPage = index < rows.length - 1;
      if (moreInPage || next !== undefined) {
        await sleep(throttleMs);
      }
    }

    if (aborted) break;

    if (next === undefined) {
      // A pending session that vanished from the list (deleted or archived)
      // would otherwise block completion forever. Skip this on a resumed walk:
      // unseen ids may simply be on pages before the resume cursor.
      if (startedFromBeginning) {
        for (const id of [...pending]) {
          if (seen.has(id)) continue;
          const count = (attempts.get(id) ?? 0) + 1;
          attempts.set(id, count);
          if (count >= MAX_ATTEMPTS) pending.delete(id);
        }
      }
      return finish(true);
    }
    if (next === cursor) {
      onError(new Error('backfill: session.list returned a repeating cursor'));
      state.nextCursor = undefined;
      return finish(false);
    }

    cursor = next;
    state.nextCursor = cursor;
    await persist();
  }

  await persist();
  return { sessions, written, failed, pages, skipped: false };
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
    !isCount(state.sessions) ||
    !isCount(state.written) ||
    !isCount(state.failed)
  ) {
    return undefined;
  }
  if (state.nextCursor !== undefined && !isCursor(state.nextCursor)) return undefined;
  if (state.completedAt !== undefined && typeof state.completedAt !== 'string') {
    return undefined;
  }
  if (state.pending !== undefined) {
    if (!Array.isArray(state.pending) || !state.pending.every(isCursor)) return undefined;
  }
  if (state.attempts !== undefined) {
    if (typeof state.attempts !== 'object' || state.attempts === null) return undefined;
    for (const value of Object.values(state.attempts as Record<string, unknown>)) {
      if (!isCount(value)) return undefined;
    }
  }
  return state as unknown as BackfillState;
}

function isCount(value: unknown): value is number {
  return typeof value === 'number' && Number.isInteger(value) && value >= 0;
}

function isCursor(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0;
}

function defaultSleep(ms: number): Promise<void> {
  if (ms <= 0) return Promise.resolve();
  return new Promise((resolve) => setTimeout(resolve, ms));
}
