import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createEventRouter, type CaptureLike } from '../events.ts';

interface Timer {
  readonly callback: () => void;
  readonly ms: number;
}

function scheduler() {
  const timers = new Map<number, Timer>();
  let next = 0;
  return {
    timers,
    setTimeout: (callback: () => void, ms: number) => {
      const id = ++next;
      timers.set(id, { callback, ms });
      return id;
    },
    clearTimeout: (handle: unknown) => {
      timers.delete(handle as number);
    },
    async fireAll() {
      const pending = [...timers.values()];
      timers.clear();
      for (const timer of pending) timer.callback();
      await flush();
    },
  };
}

async function flush() {
  await new Promise((resolve) => setImmediate(resolve));
}

function fakeCapture() {
  const calls: Array<{ sessionID: string; final: boolean }> = [];
  const capture: CaptureLike = {
    async capture(sessionID, options) {
      calls.push({ sessionID, final: options.final });
      return { written: true, path: '/spool/x.json' };
    },
  };
  return { capture, calls };
}

test('idle waits for the 3–5 minute debounce before capturing a live bundle', async () => {
  const clock = scheduler();
  const { capture, calls } = fakeCapture();
  const router = createEventRouter({ capture, debounceMs: 240_000, ...clock });

  router.handle({ type: 'session.idle', data: { sessionID: 'ses_1' } });
  assert.equal(calls.length, 0);
  assert.equal(clock.timers.size, 1);
  assert.equal([...clock.timers.values()][0]?.ms, 240_000);

  await clock.fireAll();
  assert.deepEqual(calls, [{ sessionID: 'ses_1', final: false }]);
  await router.dispose();
});

test('repeated idle events reset the debounce and capture once', async () => {
  const clock = scheduler();
  const { capture, calls } = fakeCapture();
  const router = createEventRouter({ capture, debounceMs: 240_000, ...clock });

  router.handle({ type: 'session.idle', data: { sessionID: 'ses_1' } });
  router.handle({ type: 'session.idle', data: { sessionID: 'ses_1' } });
  assert.equal(clock.timers.size, 1);

  await clock.fireAll();
  assert.equal(calls.length, 1);
  await router.dispose();
});

test('compaction.started captures immediately and cancels the pending idle timer', async () => {
  const clock = scheduler();
  const { capture, calls } = fakeCapture();
  const router = createEventRouter({ capture, debounceMs: 240_000, ...clock });

  router.handle({ type: 'session.idle', data: { sessionID: 'ses_1' } });
  router.handle({ type: 'session.compaction.started', data: { sessionID: 'ses_1' } });
  assert.equal(clock.timers.size, 0);
  await flush();
  assert.deepEqual(calls, [{ sessionID: 'ses_1', final: false }]);
  await router.dispose();
});

test('execution failures and interruptions are captured as final', async () => {
  const clock = scheduler();
  const { capture, calls } = fakeCapture();
  const router = createEventRouter({ capture, debounceMs: 240_000, ...clock });

  router.handle({ type: 'session.execution.failed', data: { sessionID: 'ses_1' } });
  router.handle({ type: 'session.execution.interrupted', data: { sessionID: 'ses_2' } });
  await flush();
  assert.deepEqual(calls, [
    { sessionID: 'ses_1', final: true },
    { sessionID: 'ses_2', final: true },
  ]);
  await router.dispose();
});

test('ignores unrelated events', async () => {
  const clock = scheduler();
  const { capture, calls } = fakeCapture();
  const router = createEventRouter({ capture, debounceMs: 240_000, ...clock });

  router.handle({ type: 'session.updated', data: { sessionID: 'ses_1' } });
  await flush();
  assert.equal(calls.length, 0);
  assert.equal(clock.timers.size, 0);
  await router.dispose();
});

test('capture errors are reported without breaking later captures', async () => {
  const clock = scheduler();
  const errors: unknown[] = [];
  let fail = true;
  const calls: string[] = [];
  const router = createEventRouter({
    capture: {
      async capture(sessionID) {
        calls.push(sessionID);
        if (fail) throw new Error('disk full');
        return { written: true, path: '/spool/x.json' };
      },
    },
    debounceMs: 240_000,
    onError: (error) => errors.push(error),
    ...clock,
  });

  router.handle({ type: 'session.idle', data: { sessionID: 'ses_1' } });
  await clock.fireAll();
  assert.equal(errors.length, 1);

  // The same idle session may retry on the next event cycle.
  fail = false;
  router.handle({ type: 'session.idle', data: { sessionID: 'ses_1' } });
  await clock.fireAll();
  assert.deepEqual(calls, ['ses_1', 'ses_1']);
  await router.dispose();
});

test('dispose flushes pending idle work before unload', async () => {
  const clock = scheduler();
  const { capture, calls } = fakeCapture();
  const router = createEventRouter({ capture, debounceMs: 240_000, ...clock });

  router.handle({ type: 'session.idle', data: { sessionID: 'ses_1' } });
  await router.dispose();
  assert.equal(clock.timers.size, 0);
  assert.deepEqual(calls, [{ sessionID: 'ses_1', final: false }]);
});

test('flush captures a pending idle session immediately', async () => {
  const clock = scheduler();
  const { capture, calls } = fakeCapture();
  const router = createEventRouter({ capture, debounceMs: 240_000, ...clock });

  router.handle({ type: 'session.idle', data: { sessionID: 'ses_1' } });
  await router.flush('ses_1');
  assert.equal(clock.timers.size, 0);
  assert.deepEqual(calls, [{ sessionID: 'ses_1', final: false }]);
  await router.dispose();
});

test('flush retries a failed write once', async () => {
  const clock = scheduler();
  const errors: unknown[] = [];
  let attempts = 0;
  const router = createEventRouter({
    debounceMs: 240_000,
    onError: (error) => errors.push(error),
    ...clock,
    capture: {
      async capture() {
        attempts += 1;
        if (attempts === 1) throw new Error('disk full');
        return { written: true, path: '/spool/x.json' };
      },
    },
  });

  await router.flush('ses_1');
  assert.equal(attempts, 2);
  assert.equal(errors.length, 1);
  await router.dispose();
});

test('captures of one session run sequentially', async () => {
  const events: string[] = [];
  const deferred: Array<() => void> = [];
  const router = createEventRouter({
    debounceMs: 240_000,
    setTimeout: () => 1,
    clearTimeout: () => undefined,
    capture: {
      async capture(sessionID, options) {
        events.push(`start:${options.final}`);
        if (!options.final) {
          await new Promise<void>((resolve) => deferred.push(resolve));
        }
        events.push(`end:${options.final}`);
        return { written: true, path: '/spool/x.json' };
      },
    },
  });

  router.handle({ type: 'session.compaction.started', data: { sessionID: 'ses_1' } });
  await flush();
  router.handle({ type: 'session.execution.failed', data: { sessionID: 'ses_1' } });
  await flush();
  assert.deepEqual(events, ['start:false']);

  deferred[0]?.();
  await flush();
  assert.deepEqual(events, ['start:false', 'end:false', 'start:true', 'end:true']);
  await router.dispose();
});
