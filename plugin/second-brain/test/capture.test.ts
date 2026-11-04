import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createCapture, type CaptureDeps } from '../capture.ts';
import { defaultConfig } from '../config.ts';
import type { SessionMessageLike, SpoolBundle } from '../types.ts';

const SESSION = {
  id: 'ses_01JABCDEF',
  projectID: 'prj_1',
  title: 'Fix migration ordering',
  agent: 'build',
  model: { providerID: 'anthropic', id: 'claude-sonnet' },
  time: { created: Date.parse('2026-11-03T12:00:00Z'), updated: Date.parse('2026-11-03T12:30:00Z') },
  location: { directory: '/repo' },
};

const MESSAGES: SessionMessageLike[] = [
  { id: 'msg_1', type: 'user', time: { created: Date.parse('2026-11-03T12:00:10Z') }, text: 'Why 004 first?' },
  {
    id: 'msg_2',
    type: 'assistant',
    time: { created: Date.parse('2026-11-03T12:00:20Z') },
    content: [{ type: 'text', text: 'Filenames sort lexicographically.' }],
  },
];

interface Harness {
  deps: CaptureDeps;
  writes: Array<{ dir: string; bundle: SpoolBundle }>;
  store: Map<string, unknown>;
}

function harness(options: { deps?: Partial<CaptureDeps>; failWrite?: boolean } = {}): Harness {
  const store = new Map<string, unknown>();
  const writes: Harness['writes'] = [];
  const failWrite = options.failWrite ?? false;

  const deps: CaptureDeps = {
    config: { ...defaultConfig('/config/second-brain'), spoolDir: '/spool' },
    directory: '/repo',
    hostname: 'mac',
    opencodeVersion: '2.2.5',
    sessions: {
      get: async () => SESSION,
      context: async () => MESSAGES,
    },
    storage: {
      get: async (key) => store.get(key),
      set: async (key, value) => {
        store.set(key, value);
      },
    },
    findRemote: async () => 'git@github.com:SeRGei93/meme.git',
    vcs: { branch: async () => 'main' },
    now: () => new Date('2026-11-03T12:34:56.789Z'),
    uuid: (() => {
      let n = 0;
      return () => `uuid-${++n}`;
    })(),
    write: async ({ dir, bundle }) => {
      if (failWrite) throw new Error('disk full');
      writes.push({ dir, bundle });
      return `${dir}/bundle-${writes.length}.json`;
    },
    ...options.deps,
  };

  return { deps, writes, store };
}

test('captures a live session into the spool and records the cursor', async () => {
  const h = harness();
  const capture = createCapture(h.deps);
  const result = await capture.capture('ses_01JABCDEF', { final: false });

  assert.deepEqual(result, { written: true, path: '/spool/bundle-1.json' });
  assert.equal(h.writes.length, 1);
  const bundle = h.writes[0]?.bundle;
  assert.equal(bundle?.final, false);
  assert.equal(bundle?.project_key, 'github.com/SeRGei93/meme');
  assert.equal(bundle?.project_path, '/repo');
  assert.equal(bundle?.metadata.branch, 'main');
  assert.ok(h.store.get('capture/session/ses_01JABCDEF'));
});

test('skips an unchanged live capture but writes again when final is requested', async () => {
  const h = harness();
  const capture = createCapture(h.deps);

  await capture.capture('ses_01JABCDEF', { final: false });
  const duplicate = await capture.capture('ses_01JABCDEF', { final: false });
  assert.deepEqual(duplicate, { written: false, reason: 'unchanged' });
  assert.equal(h.writes.length, 1);

  const final = await capture.capture('ses_01JABCDEF', { final: true });
  assert.equal(final.written, true);
  assert.equal(h.writes[1]?.bundle.final, true);

  const finalAgain = await capture.capture('ses_01JABCDEF', { final: true });
  assert.deepEqual(finalAgain, { written: false, reason: 'unchanged' });
  assert.equal(h.writes.length, 2);
});

test('does not move the cursor when the write fails, then retries on the next capture', async () => {
  const h = harness({ failWrite: true });
  const capture = createCapture(h.deps);

  await assert.rejects(capture.capture('ses_01JABCDEF', { final: false }), /disk full/);
  assert.equal(h.store.has('capture/session/ses_01JABCDEF'), false);
  assert.equal(h.writes.length, 0);

  const captureRetry = createCapture({
    ...h.deps,
    write: async ({ dir, bundle }) => {
      h.writes.push({ dir, bundle });
      return `${dir}/bundle-1.json`;
    },
  });
  const retry = await captureRetry.capture('ses_01JABCDEF', { final: false });
  assert.equal(retry.written, true);
  assert.ok(h.store.get('capture/session/ses_01JABCDEF'));
});

test('skips a session without usable messages and leaves the cursor empty', async () => {
  const h = harness({
    deps: {
      sessions: {
        get: async () => SESSION,
        context: async () => [{ id: 'msg_1', type: 'system', time: { created: 1 }, text: 'boot' }],
      },
    },
  });
  const capture = createCapture(h.deps);
  assert.deepEqual(await capture.capture('ses_01JABCDEF', { final: true }), {
    written: false,
    reason: 'empty',
  });
  assert.equal(h.writes.length, 0);
  assert.equal(h.store.size, 0);
});

test('writes a final bundle after a later live capture of the same session', async () => {
  let messages: SessionMessageLike[] = [MESSAGES[0]!];
  const h = harness({
    deps: {
      sessions: {
        get: async () => SESSION,
        context: async () => messages,
      },
    },
  });
  const capture = createCapture(h.deps);

  // A failure finalizes the first version.
  await capture.capture('ses_01JABCDEF', { final: true });
  assert.equal(h.writes[0]?.bundle.final, true);

  // The session continues, so the new content is written as live again.
  messages = [...messages, MESSAGES[1]!];
  await capture.capture('ses_01JABCDEF', { final: false });
  assert.equal(h.writes[1]?.bundle.final, false);

  // A later failure with unchanged content must still publish a final bundle.
  await capture.capture('ses_01JABCDEF', { final: true });
  assert.equal(h.writes.length, 3);
  assert.equal(h.writes[2]?.bundle.final, true);
});

test('uses opencode:<projectID> when no git remote is found', async () => {
  const h = harness({ deps: { findRemote: async () => undefined } });
  const capture = createCapture(h.deps);
  await capture.capture('ses_01JABCDEF', { final: false });
  assert.equal(h.writes[0]?.bundle.project_key, 'opencode:prj_1');
});

test('falls back to the session directory when location is missing', async () => {
  const h = harness({
    deps: {
      directory: '/fallback',
      sessions: {
        get: async () => ({ ...SESSION, location: undefined }),
        context: async () => MESSAGES,
      },
    },
  });
  const capture = createCapture(h.deps);
  await capture.capture('ses_01JABCDEF', { final: false });
  assert.equal(h.writes[0]?.bundle.project_path, '/fallback');
});
