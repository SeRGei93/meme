import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runBackfill, type BackfillDeps, type BackfillState } from '../backfill.ts';
import type { CaptureResult } from '../capture.ts';
import type { SessionInfoLike } from '../types.ts';

interface Page {
  readonly ids: readonly string[];
  readonly next?: string;
}

interface Harness {
  readonly deps: BackfillDeps;
  readonly store: Map<string, unknown>;
  readonly captures: Array<{ id: string; final: boolean }>;
  readonly listCalls: Array<{ directory: string; limit?: number; cursor?: string }>;
  readonly sleeps: number[];
  readonly errors: unknown[];
}

const SESSION: Omit<SessionInfoLike, 'id'> = {
  projectID: 'prj_1',
  title: 'Fix migration ordering',
  agent: 'build',
  model: { providerID: 'anthropic', id: 'claude-sonnet' },
  time: { created: Date.parse('2026-11-03T12:00:00Z'), updated: Date.parse('2026-11-03T12:30:00Z') },
  location: { directory: '/repo' },
};

function harness(
  pages: Record<string, Page>,
  options: {
    failCapture?: (id: string) => boolean;
    failPage?: (cursor: string | undefined) => boolean;
    throttleMs?: number;
    sleep?: (ms: number) => Promise<void>;
    signal?: AbortSignal;
  } = {},
): Harness {
  const store = new Map<string, unknown>();
  const captures: Harness['captures'] = [];
  const listCalls: Harness['listCalls'] = [];
  const sleeps: number[] = [];
  const errors: unknown[] = [];
  const captured = new Set<string>();

  const deps: BackfillDeps = {
    sessions: {
      async list(input) {
        listCalls.push({ directory: input.directory, limit: input.limit, cursor: input.cursor });
        if (options.failPage?.(input.cursor)) {
          throw new Error(`list failed for ${input.cursor ?? 'start'}`);
        }
        const page = pages[input.cursor ?? 'start'];
        if (!page) throw new Error(`unknown cursor ${input.cursor}`);
        return {
          data: page.ids.map((id) => ({ ...SESSION, id })),
          cursor: { next: page.next },
        };
      },
    },
    capture: {
      async capture(sessionID, captureOptions): Promise<CaptureResult> {
        captures.push({ id: sessionID, final: captureOptions.final });
        if (options.failCapture?.(sessionID) && !captured.has(sessionID)) {
          throw new Error(`capture failed for ${sessionID}`);
        }
        if (captured.has(sessionID)) return { written: false, reason: 'unchanged' };
        captured.add(sessionID);
        return { written: true, path: `/spool/${sessionID}.json` };
      },
    },
    storage: {
      async get(key) {
        return store.get(key);
      },
      async set(key, value) {
        store.set(key, value);
      },
    },
    directory: '/repo',
    pageSize: 2,
    throttleMs: options.throttleMs ?? 0,
    sleep:
      options.sleep ??
      (async (ms) => {
        sleeps.push(ms);
      }),
    now: () => new Date('2026-11-04T20:00:00Z'),
    onError: (error) => errors.push(error),
    signal: options.signal,
  };

  return { deps, store, captures, listCalls, sleeps, errors };
}

function stateFrom(store: Map<string, unknown>): BackfillState | undefined {
  for (const [key, value] of store) {
    if (key.startsWith('backfill/v1/')) return value as BackfillState;
  }
  return undefined;
}

test('backfill walks every page and writes one final bundle per session', async () => {
  const h = harness({
    start: { ids: ['a', 'b'], next: 'p2' },
    p2: { ids: ['c'], next: 'p3' },
    p3: { ids: ['d'] },
  });

  const result = await runBackfill(h.deps);

  assert.deepEqual(h.listCalls.map((call) => call.cursor), [undefined, 'p2', 'p3']);
  assert.ok(h.listCalls.every((call) => call.directory === '/repo'));
  assert.deepEqual(
    h.captures.map((capture) => capture.id),
    ['a', 'b', 'c', 'd'],
  );
  assert.ok(h.captures.every((capture) => capture.final));
  assert.equal(result.sessions, 4);
  assert.equal(result.written, 4);
  assert.equal(result.failed, 0);
  assert.equal(result.pages, 3);
  assert.equal(result.skipped, false);

  const state = stateFrom(h.store);
  assert.equal(state?.completedAt, '2026-11-04T20:00:00.000Z');
  assert.equal(state?.nextCursor, undefined);
  assert.equal(state?.sessions, 4);
});

test('a completed backfill is skipped on the next launch', async () => {
  const pages = {
    start: { ids: ['a', 'b'], next: 'p2' },
    p2: { ids: ['c'] },
  };
  const h = harness(pages);

  await runBackfill(h.deps);
  const capturesAfterFirstRun = h.captures.length;
  const listCallsAfterFirstRun = h.listCalls.length;

  const second = await runBackfill(h.deps);

  assert.equal(second.skipped, true);
  assert.equal(h.captures.length, capturesAfterFirstRun);
  assert.equal(h.listCalls.length, listCallsAfterFirstRun);
});

test('an aborted backfill resumes from the saved page cursor', async () => {
  const controller = new AbortController();
  let sleepCount = 0;
  const h = harness(
    {
      start: { ids: ['a', 'b'], next: 'p2' },
      p2: { ids: ['c'] },
    },
    {
      sleep: async () => {
        sleepCount += 1;
        if (sleepCount === 2) controller.abort();
      },
      signal: controller.signal,
    },
  );

  const aborted = await runBackfill(h.deps);

  assert.equal(aborted.skipped, false);
  assert.deepEqual(h.captures.map((capture) => capture.id), ['a', 'b']);
  const saved = stateFrom(h.store);
  assert.equal(saved?.completedAt, undefined);
  assert.equal(saved?.nextCursor, 'p2');

  const resumed = await runBackfill({ ...h.deps, signal: new AbortController().signal });

  assert.equal(resumed.skipped, false);
  assert.equal(h.listCalls.at(-1)?.cursor, 'p2');
  assert.deepEqual(h.captures.map((capture) => capture.id), ['a', 'b', 'c']);
  assert.equal(stateFrom(h.store)?.completedAt, '2026-11-04T20:00:00.000Z');
});

test('a failing session does not stop the rest and is retried on the next run', async () => {
  let failB = true;
  const h = harness(
    {
      start: { ids: ['a', 'b', 'c'] },
    },
    { failCapture: (id) => id === 'b' && failB },
  );

  const result = await runBackfill(h.deps);

  assert.deepEqual(h.captures.map((capture) => capture.id), ['a', 'b', 'c']);
  assert.equal(result.written, 2);
  assert.equal(result.failed, 1);
  assert.equal(h.errors.length, 1);
  const state = stateFrom(h.store);
  assert.equal(state?.completedAt, undefined, 'failed backfill must retry next launch');
  assert.equal(state?.failed, 1);

  // The next run retries the failed session; already written ones are unchanged.
  failB = false;
  const retry = await runBackfill(h.deps);

  assert.equal(retry.failed, 0);
  assert.equal(stateFrom(h.store)?.completedAt, '2026-11-04T20:00:00.000Z');
});

test('backfill throttles between sessions', async () => {
  const h = harness(
    { start: { ids: ['a', 'b', 'c'] } },
    { throttleMs: 25 },
  );

  await runBackfill(h.deps);

  assert.deepEqual(h.sleeps, [25, 25, 25]);
});

test('a page listing error keeps the backfill incomplete', async () => {
  const h = harness(
    {
      start: { ids: ['a'], next: 'p2' },
      p2: { ids: ['b'] },
    },
    { failPage: (cursor) => cursor === 'p2' },
  );

  const result = await runBackfill(h.deps);

  assert.equal(result.pages, 1);
  assert.equal(h.errors.length, 1);
  assert.equal(stateFrom(h.store)?.completedAt, undefined);
  assert.deepEqual(h.captures.map((capture) => capture.id), ['a']);
});

test('a repeated cursor ends the walk instead of looping forever', async () => {
  const h = harness({
    start: { ids: ['a'], next: 'p2' },
    p2: { ids: ['b'], next: 'p2' },
  });

  const result = await runBackfill(h.deps);

  assert.equal(result.pages, 2);
  assert.equal(result.sessions, 2);
  assert.equal(h.listCalls.length, 2);
});
