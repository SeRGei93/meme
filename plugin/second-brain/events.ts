import type { Capture } from './capture.ts';

export interface EventLike {
  readonly type: string;
  readonly data: { readonly sessionID: string };
}

export type CaptureLike = Pick<Capture, 'capture'>;

export interface EventRouterDeps {
  readonly capture: CaptureLike;
  readonly debounceMs: number;
  readonly setTimeout?: (callback: () => void, ms: number) => unknown;
  readonly clearTimeout?: (handle: unknown) => void;
  readonly onError?: (error: unknown) => void;
}

export interface EventRouter {
  handle(event: EventLike): void;
  /** Captures a pending idle session immediately (used before compaction). */
  flush(sessionID: string): Promise<void>;
  /** Flushes pending idle sessions, then stops accepting events. */
  dispose(): Promise<void>;
}

export const IDLE_EVENT = 'session.idle';
export const COMPACTION_EVENT = 'session.compaction.started';
export const FAILED_EVENT = 'session.execution.failed';
export const INTERRUPTED_EVENT = 'session.execution.interrupted';

export const CAPTURE_EVENTS = [IDLE_EVENT, COMPACTION_EVENT, FAILED_EVENT, INTERRUPTED_EVENT] as const;

/**
 * Maps OpenCode events to captures:
 *   - session.idle               -> debounced live capture (final=false)
 *   - session.compaction.started -> immediate capture before compaction
 *   - session.execution.failed   -> immediate final capture
 *   - session.execution.interrupted -> immediate final capture
 *
 * Captures for one session are serialized so a compaction right after idle
 * cannot interleave with the pending live write. The queue tail never rejects,
 * so one failed write does not block later ones.
 */
export function createEventRouter(deps: EventRouterDeps): EventRouter {
  const setTimer = deps.setTimeout ?? ((callback, ms) => setTimeout(callback, ms));
  const clearTimer = deps.clearTimeout ?? ((handle) => clearTimeout(handle as ReturnType<typeof setTimeout>));
  const idleTimers = new Map<string, unknown>();
  const queues = new Map<string, Promise<void>>();
  let disposed = false;

  function enqueue(sessionID: string, final: boolean): Promise<void> {
    const previous = queues.get(sessionID) ?? Promise.resolve();
    const attempt = previous.then(() => deps.capture.capture(sessionID, { final }));
    const tail = attempt.then(
      () => undefined,
      () => undefined,
    );
    const queued = tail.finally(() => {
      if (queues.get(sessionID) === queued) queues.delete(sessionID);
    });
    queues.set(sessionID, queued);
    return attempt.then(() => undefined);
  }

  function dispatch(sessionID: string, final: boolean): void {
    if (disposed) return;
    void enqueue(sessionID, final).catch((error) => deps.onError?.(error));
  }

  function cancelIdle(sessionID: string): void {
    const handle = idleTimers.get(sessionID);
    if (handle === undefined) return;
    clearTimer(handle);
    idleTimers.delete(sessionID);
  }

  return {
    handle(event) {
      if (disposed) return;
      const sessionID = event.data?.sessionID;
      if (!sessionID) return;

      switch (event.type) {
        case IDLE_EVENT: {
          cancelIdle(sessionID);
          const handle = setTimer(() => {
            idleTimers.delete(sessionID);
            dispatch(sessionID, false);
          }, deps.debounceMs);
          idleTimers.set(sessionID, handle);
          return;
        }
        case COMPACTION_EVENT:
          cancelIdle(sessionID);
          dispatch(sessionID, false);
          return;
        case FAILED_EVENT:
        case INTERRUPTED_EVENT:
          cancelIdle(sessionID);
          dispatch(sessionID, true);
          return;
        default:
          return;
      }
    },

    async flush(sessionID) {
      cancelIdle(sessionID);
      try {
        await enqueue(sessionID, false);
      } catch (error) {
        // One retry makes transient rename failures less likely to let
        // compaction proceed without a pre-compaction bundle.
        deps.onError?.(error);
        await enqueue(sessionID, false);
      }
    },

    async dispose() {
      // A user quitting OpenCode inside the debounce window should not lose the
      // session: flush pending idle captures before shutting the plugin down.
      for (const sessionID of [...idleTimers.keys()]) {
        cancelIdle(sessionID);
        dispatch(sessionID, false);
      }
      disposed = true;
      await Promise.allSettled([...queues.values()]);
      queues.clear();
    },
  };
}
