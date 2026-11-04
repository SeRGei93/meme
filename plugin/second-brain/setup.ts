import { hostname as osHostname } from 'node:os';
import { createCapture, type WriteBundle } from './capture.ts';
import { loadConfig, type PluginConfig } from './config.ts';
import type { StorageLike } from './cursor.ts';
import { CAPTURE_EVENTS, createEventRouter, type EventLike } from './events.ts';
import { findGitRemote } from './project.ts';
import type { SessionInfoLike, SessionMessageLike } from './types.ts';

/** The OpenCode subset this plugin needs; index.ts adapts the real SDK to it. */
export interface SetupSession {
  get(input: { sessionID: string }): Promise<SessionInfoLike>;
  context(input: { sessionID: string }): Promise<readonly SessionMessageLike[]>;
  hook(
    name: 'compaction',
    callback: (event: { sessionID: string }) => Promise<void>,
  ): Promise<{ dispose(): Promise<void> }>;
}

export interface SetupContext {
  readonly app: { readonly version: string };
  readonly location: { readonly directory: string; readonly project: { readonly id: string } };
  readonly session: SetupSession;
  readonly storage: StorageLike;
  readonly vcs: {
    get(input: { location: { directory: string } }): Promise<{ data: { branch: { current?: string } } }>;
  };
  readonly event: {
    subscribe(options: { types: readonly string[]; signal: AbortSignal }): AsyncIterable<EventLike>;
  };
}

export interface SetupOptions {
  readonly config?: PluginConfig;
  readonly hostname?: string;
  readonly findRemote?: (directory: string) => Promise<string | undefined>;
  readonly now?: () => Date;
  readonly uuid?: () => string;
  readonly write?: WriteBundle;
  readonly onError?: (error: unknown) => void;
}

export type Cleanup = () => Promise<void>;

export function logError(error: unknown): void {
  const message = error instanceof Error ? error.message : String(error);
  console.error(`[second-brain] capture failed: ${message}`);
}

/**
 * Wires OpenCode events, the compaction hook and session reads into the tested
 * capture core. The plugin never talks to the network; the macOS app uploads
 * bundles from the spool.
 */
export async function setupPlugin(ctx: SetupContext, options: SetupOptions = {}): Promise<Cleanup> {
  const onError = options.onError ?? logError;
  const config = options.config ?? (await loadConfig());
  const capture = createCapture({
    sessions: {
      get: (input) => ctx.session.get(input),
      context: (input) => ctx.session.context(input),
    },
    storage: ctx.storage,
    config,
    directory: ctx.location.directory,
    projectID: ctx.location.project.id,
    hostname: options.hostname ?? osHostname(),
    opencodeVersion: ctx.app.version,
    vcs: {
      branch: async (directory) => {
        const info = await ctx.vcs.get({ location: { directory } });
        return info.data.branch.current;
      },
    },
    findRemote: options.findRemote ?? findGitRemote,
    now: options.now,
    uuid: options.uuid,
    write: options.write,
    onError,
  });

  const router = createEventRouter({
    capture,
    debounceMs: config.idleDebounceMs,
    onError,
  });

  // The event tells us compaction started, but only a hook can hold the
  // compaction model call until the pre-compaction bundle is written.
  const compactionHook = await ctx.session.hook('compaction', async (event) => {
    await router.flush(event.sessionID).catch(onError);
  });

  const controller = new AbortController();
  void (async () => {
    try {
      for await (const event of ctx.event.subscribe({
        types: [...CAPTURE_EVENTS],
        signal: controller.signal,
      })) {
        router.handle(event);
      }
    } catch (error) {
      if (!controller.signal.aborted) onError(error);
    }
  })();

  return async () => {
    controller.abort();
    try {
      await compactionHook.dispose();
    } finally {
      await router.dispose();
    }
  };
}
