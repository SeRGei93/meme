import { createHash } from 'node:crypto';
import type { SpoolBundle } from './types.ts';

export interface CursorState {
  readonly fingerprint: string;
  readonly final: boolean;
}

export interface StorageLike {
  get(key: string): Promise<unknown>;
  set(key: string, value: unknown): Promise<void>;
}

/**
 * Per-session capture state kept in ctx.storage. The cursor is written only
 * after the bundle is safely renamed into the spool, so a failed write is
 * retried on the next event instead of being silently skipped.
 */
export function createCursor(storage: StorageLike) {
  return {
    async get(sessionID: string): Promise<CursorState | undefined> {
      const value = await storage.get(storageKey(sessionID));
      return isCursorState(value) ? value : undefined;
    },
    async set(sessionID: string, state: CursorState): Promise<void> {
      await storage.set(storageKey(sessionID), state);
    },
  };
}

export function fingerprint(bundle: SpoolBundle): string {
  return createHash('sha256')
    .update(
      JSON.stringify({
        title: bundle.title,
        project_key: bundle.project_key,
        project_path: bundle.project_path,
        messages: bundle.messages,
      }),
    )
    .digest('hex');
}

function storageKey(sessionID: string): string {
  return `capture/session/${sessionID}`;
}

function isCursorState(value: unknown): value is CursorState {
  if (typeof value !== 'object' || value === null) return false;
  const state = value as Record<string, unknown>;
  return typeof state.fingerprint === 'string' && typeof state.final === 'boolean';
}
