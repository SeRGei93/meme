import { randomUUID } from 'node:crypto';
import { normalizeSession } from './normalize.ts';
import { createCursor, fingerprint, type StorageLike } from './cursor.ts';
import { resolveProjectKey } from './project.ts';
import { writeBundle, type WriteBundleOptions } from './spool.ts';
import type { PluginConfig } from './config.ts';
import type { SessionInfoLike, SessionMessageLike } from './types.ts';

export interface CaptureSessions {
  get(input: { sessionID: string }): Promise<SessionInfoLike>;
  context(input: { sessionID: string }): Promise<readonly SessionMessageLike[]>;
}

export interface CaptureVcs {
  branch(directory: string): Promise<string | undefined>;
}

export type WriteBundle = (options: WriteBundleOptions) => Promise<string>;

export interface CaptureDeps {
  readonly sessions: CaptureSessions;
  readonly storage: StorageLike;
  readonly config: PluginConfig;
  /** ctx.location.directory, used when a session has no location. */
  readonly directory: string;
  readonly projectID?: string;
  readonly hostname: string;
  readonly opencodeVersion?: string;
  readonly vcs?: CaptureVcs;
  readonly findRemote?: (directory: string) => Promise<string | undefined>;
  readonly write?: WriteBundle;
  readonly now?: () => Date;
  readonly uuid?: () => string;
  readonly onError?: (error: unknown) => void;
}

export interface CaptureOptions {
  readonly final: boolean;
}

export type CaptureResult =
  | { readonly written: true; readonly path: string }
  | { readonly written: false; readonly reason: 'empty' | 'unchanged' };

export interface Capture {
  capture(sessionID: string, options: CaptureOptions): Promise<CaptureResult>;
}

const defaultFindRemote = async () => undefined;

/**
 * One capture = read the session, normalize it, write a bundle, then advance
 * the cursor. Order matters: the cursor moves only after a successful rename,
 * so a failed write is retried instead of becoming a lost session.
 */
export function createCapture(deps: CaptureDeps): Capture {
  const cursor = createCursor(deps.storage);
  const write = deps.write ?? writeBundle;
  const findRemote = deps.findRemote ?? defaultFindRemote;
  const now = deps.now ?? (() => new Date());
  const newUUID = deps.uuid ?? randomUUID;
  const remoteCache = new Map<string, string | undefined>();

  return {
    async capture(sessionID, options) {
      const session = await deps.sessions.get({ sessionID });
      const messages = await deps.sessions.context({ sessionID });
      const projectPath = session.location?.directory ?? deps.directory;

      const projectKey = resolveProjectKey({
        remoteUrl: await cachedRemote(findRemote, remoteCache, projectPath),
        projectID: session.projectID ?? deps.projectID,
        directory: projectPath,
        hostname: deps.hostname,
      });

      let branch: string | undefined;
      try {
        branch = await deps.vcs?.branch(projectPath);
      } catch (error) {
        // A VCS hiccup must not drop the session; branch is informational.
        deps.onError?.(error);
      }

      const uuid = newUUID();
      const bundle = normalizeSession({
        bundleId: uuid,
        session,
        messages,
        final: options.final,
        projectKey,
        projectPath,
        branch,
        opencodeVersion: deps.opencodeVersion,
        limits: deps.config,
      });
      if (!bundle) return { written: false, reason: 'empty' };

      const hash = fingerprint(bundle);
      const previous = await cursor.get(sessionID);
      if (previous && previous.fingerprint === hash && (previous.final || !options.final)) {
        return { written: false, reason: 'unchanged' };
      }

      const path = await write({ dir: deps.config.spoolDir, bundle, now: now(), uuid });
      await cursor.set(sessionID, { fingerprint: hash, final: options.final });
      return { written: true, path };
    },
  };
}

async function cachedRemote(
  findRemote: (directory: string) => Promise<string | undefined>,
  cache: Map<string, string | undefined>,
  directory: string,
): Promise<string | undefined> {
  if (cache.has(directory)) return cache.get(directory);
  let remote: string | undefined;
  try {
    remote = await findRemote(directory);
  } catch {
    remote = undefined;
  }
  cache.set(directory, remote);
  return remote;
}
