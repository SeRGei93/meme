import { randomUUID } from 'node:crypto';
import { mkdir, rename, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import type { SpoolBundle } from './types.ts';

export interface SpoolFilesystem {
  mkdir(path: string, options: { recursive: true }): Promise<unknown>;
  writeFile(path: string, data: string, options: { mode: number }): Promise<unknown>;
  rename(from: string, to: string): Promise<unknown>;
  rm(path: string, options: { force: true }): Promise<unknown>;
}

const DEFAULT_FS: SpoolFilesystem = {
  mkdir: (path, options) => mkdir(path, options),
  writeFile: (path, data, options) => writeFile(path, data, options),
  rename: (from, to) => rename(from, to),
  rm: (path, options) => rm(path, options),
};

export interface WriteBundleOptions {
  readonly dir: string;
  readonly bundle: SpoolBundle;
  readonly now?: Date;
  readonly uuid?: string;
  readonly fs?: SpoolFilesystem;
}

/**
 * Writes a bundle atomically: full JSON goes to a sibling *.tmp file, then a
 * rename publishes it. The app watches for rename events and ignores *.tmp,
 * so a crash mid-write can never leave a half-valid bundle in the spool.
 * Returns the final path.
 */
export async function writeBundle(options: WriteBundleOptions): Promise<string> {
  const fs = options.fs ?? DEFAULT_FS;
  const now = options.now ?? new Date();
  const uuid = options.uuid ?? randomUUID();
  await fs.mkdir(options.dir, { recursive: true });

  const filename = `${formatTimestamp(now)}-${sanitize(options.bundle.external_id)}-${sanitize(uuid)}.json`;
  const finalPath = join(options.dir, filename);
  const tmpPath = `${finalPath}.tmp`;

  try {
    await fs.writeFile(tmpPath, `${JSON.stringify(options.bundle, null, 2)}\n`, { mode: 0o600 });
    await fs.rename(tmpPath, finalPath);
  } catch (error) {
    await fs.rm(tmpPath, { force: true }).catch(() => undefined);
    throw error;
  }
  return finalPath;
}

/** 2026-11-03T12:34:56.789Z -> 20261103T123456Z (filesystem-safe, sortable). */
export function formatTimestamp(date: Date): string {
  return date.toISOString().replace(/[-:]/g, '').replace(/\.\d{3}Z$/, 'Z');
}

function sanitize(value: string): string {
  return value.replace(/[^A-Za-z0-9._-]+/g, '_').slice(0, 80) || 'bundle';
}
