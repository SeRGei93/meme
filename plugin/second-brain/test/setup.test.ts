import assert from 'node:assert/strict';
import { mkdtemp, readdir, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { defaultConfig } from '../config.ts';
import { setupPlugin, type SetupContext } from '../setup.ts';
import type { EventLike } from '../events.ts';
import type { SessionMessageLike } from '../types.ts';

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

async function flushTasks() {
  await new Promise((resolve) => setImmediate(resolve));
}

async function waitFor(condition: () => Promise<boolean>, label: string) {
  for (let attempt = 0; attempt < 200; attempt++) {
    if (await condition()) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(`timed out waiting for ${label}`);
}

function fakeContext() {
  const store = new Map<string, unknown>();
  const listeners = new Set<(event: EventLike) => void>();
  let compaction: ((event: { sessionID: string }) => Promise<void>) | undefined;

  const ctx: SetupContext = {
    app: { version: '2.2.5' },
    location: { directory: '/repo', project: { id: 'prj_1' } },
    session: {
      get: async () => SESSION,
      context: async () => MESSAGES,
      hook: async (_name, callback) => {
        compaction = callback;
        return {
          dispose: async () => {
            compaction = undefined;
          },
        };
      },
    },
    storage: {
      get: async (key) => store.get(key),
      set: async (key, value) => {
        store.set(key, value);
      },
    },
    vcs: {
      get: async () => ({ data: { branch: { current: 'main' } } }),
    },
    event: {
      subscribe({ signal }) {
        return {
          async *[Symbol.asyncIterator]() {
            while (!signal.aborted) {
              const event = await new Promise<EventLike | undefined>((resolve) => {
                const onEvent = (value: EventLike) => {
                  listeners.delete(onEvent);
                  signal.removeEventListener('abort', onAbort);
                  resolve(value);
                };
                const onAbort = () => {
                  listeners.delete(onEvent);
                  resolve(undefined);
                };
                listeners.add(onEvent);
                signal.addEventListener('abort', onAbort, { once: true });
              });
              if (!event) return;
              yield event;
            }
          },
        };
      },
    },
  };

  return {
    ctx,
    emit(event: EventLike) {
      for (const listener of [...listeners]) listener(event);
    },
    compaction() {
      return compaction;
    },
  };
}

function options(spoolDir: string) {
  return {
    config: { ...defaultConfig('/config/second-brain'), spoolDir },
    hostname: 'mac',
    findRemote: async () => 'git@github.com:SeRGei93/meme.git',
  };
}

test('an execution failure event writes a final bundle through the wired plugin', async () => {
  const spoolDir = await mkdtemp(join(tmpdir(), 'sb-setup-'));
  const fake = fakeContext();
  const cleanup = await setupPlugin(fake.ctx, options(spoolDir));
  try {
    await flushTasks();
    fake.emit({ type: 'session.execution.failed', data: { sessionID: 'ses_01JABCDEF' } });
    await waitFor(async () => (await readdir(spoolDir)).length === 1, 'bundle');

    const [name] = await readdir(spoolDir);
    const bundle = JSON.parse(await readFile(join(spoolDir, name!), 'utf8'));
    assert.equal(bundle.final, true);
    assert.equal(bundle.metadata.branch, 'main');
  } finally {
    await cleanup();
    await rm(spoolDir, { recursive: true, force: true });
  }
});

test('the compaction hook writes the bundle before returning', async () => {
  const spoolDir = await mkdtemp(join(tmpdir(), 'sb-setup-'));
  const fake = fakeContext();
  const cleanup = await setupPlugin(fake.ctx, options(spoolDir));
  try {
    const hook = fake.compaction();
    assert.ok(hook);
    await hook({ sessionID: 'ses_01JABCDEF' });
    const [name] = await readdir(spoolDir);
    assert.ok(name);
    const bundle = JSON.parse(await readFile(join(spoolDir, name), 'utf8'));
    assert.equal(bundle.final, false);
  } finally {
    await cleanup();
    await rm(spoolDir, { recursive: true, force: true });
  }
});

test('cleanup flushes a session still waiting on the idle debounce', async () => {
  const spoolDir = await mkdtemp(join(tmpdir(), 'sb-setup-'));
  const fake = fakeContext();
  const cleanup = await setupPlugin(fake.ctx, options(spoolDir));
  try {
    await flushTasks();
    fake.emit({ type: 'session.idle', data: { sessionID: 'ses_01JABCDEF' } });
    await flushTasks();
    assert.equal((await readdir(spoolDir)).length, 0);

    await cleanup();
    const [name] = await readdir(spoolDir);
    assert.ok(name);
    const bundle = JSON.parse(await readFile(join(spoolDir, name), 'utf8'));
    assert.equal(bundle.final, false);
  } finally {
    await rm(spoolDir, { recursive: true, force: true });
  }
});

test('setup backfills existing sessions into the spool', async () => {
  const spoolDir = await mkdtemp(join(tmpdir(), 'sb-setup-'));
  const fake = fakeContext();
  fake.ctx.session.list = async () => ({
    data: [SESSION],
    cursor: { next: undefined },
  });
  const cleanup = await setupPlugin(fake.ctx, {
    ...options(spoolDir),
    backfill: { throttleMs: 0 },
  });
  try {
    await waitFor(async () => (await readdir(spoolDir)).length === 1, 'backfill bundle');

    const [name] = await readdir(spoolDir);
    const bundle = JSON.parse(await readFile(join(spoolDir, name!), 'utf8'));
    assert.equal(bundle.external_id, SESSION.id);
    assert.equal(bundle.final, true);
    assert.equal(bundle.source, 'opencode');
  } finally {
    await cleanup();
    await rm(spoolDir, { recursive: true, force: true });
  }
});
