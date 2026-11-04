import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtemp, readdir, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { createCapture } from '../capture.ts';
import { defaultConfig } from '../config.ts';
import type { SessionMessageLike } from '../types.ts';

const ROOT = fileURLToPath(new URL('../../../', import.meta.url));
const VALIDATOR = join(ROOT, 'tools', 'validate-spool.mjs');

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
    time: { created: Date.parse('2026-11-03T12:00:20Z'), completed: Date.parse('2026-11-03T12:00:30Z') },
    content: [
      { type: 'reasoning', text: 'not persisted' },
      { type: 'text', text: 'Filenames sort lexicographically.' },
      { type: 'tool', name: 'bash', state: { status: 'completed', input: { command: 'go test ./...' } } },
      { type: 'tool', name: 'edit', state: { status: 'completed', input: { filePath: '/repo/server/main.go' } } },
    ],
  },
];

function validate(path: string) {
  const result = spawnSync(process.execPath, [VALIDATOR, path], { encoding: 'utf8' });
  return { status: result.status, stdout: result.stdout, stderr: result.stderr };
}

test('a captured live bundle is valid against docs/spool.schema.json', async () => {
  const spoolDir = await mkdtemp(join(tmpdir(), 'sb-contract-'));
  try {
    const capture = createCapture({
      config: { ...defaultConfig('/config/second-brain'), spoolDir },
      directory: '/repo',
      hostname: 'mac',
      opencodeVersion: '2.2.5',
      sessions: { get: async () => SESSION, context: async () => MESSAGES },
      storage: (() => {
        const store = new Map<string, unknown>();
        return { get: async (key) => store.get(key), set: async (key, value) => void store.set(key, value) };
      })(),
      findRemote: async () => 'git@github.com:SeRGei93/meme.git',
      vcs: { branch: async () => 'main' },
    });

    const result = await capture.capture('ses_01JABCDEF', { final: false });
    assert.equal(result.written, true);
    const path = result.written ? result.path : '';
    const validation = validate(path);
    assert.equal(validation.status, 0, validation.stderr);
    assert.match(validation.stdout, /^valid:/m);

    const bundle = JSON.parse(await readFile(path, 'utf8'));
    assert.equal(bundle.final, false);
    assert.equal(bundle.source, 'opencode');
    assert.equal(bundle.messages.length, 2);
    assert.equal(JSON.stringify(bundle).includes('not persisted'), false);
  } finally {
    await rm(spoolDir, { recursive: true, force: true });
  }
});

test('a final bundle with tool calls is valid and survives a plugin restart', async () => {
  const spoolDir = await mkdtemp(join(tmpdir(), 'sb-contract-'));
  const store = new Map<string, unknown>();
  const deps = {
    config: { ...defaultConfig('/config/second-brain'), spoolDir },
    directory: '/repo',
    hostname: 'mac',
    sessions: { get: async () => SESSION, context: async () => MESSAGES },
    storage: { get: async (key: string) => store.get(key), set: async (key: string, value: unknown) => void store.set(key, value) },
    findRemote: async () => 'git@github.com:SeRGei93/meme.git',
  } as const;

  try {
    const before = await createCapture(deps).capture('ses_01JABCDEF', { final: true });
    assert.equal(before.written, true);
    const path = before.written ? before.path : '';
    assert.equal(validate(path).status, 0);

    const bundle = JSON.parse(await readFile(path, 'utf8'));
    assert.equal(bundle.final, true);
    assert.deepEqual(bundle.messages[1].tool_calls, [
      { name: 'bash', command: 'go test ./...' },
      { name: 'edit', path: '/repo/server/main.go' },
    ]);
    assert.match(path, /ses_01JABCDEF/);

    // A fresh plugin instance with the same storage must not duplicate an
    // unchanged final capture.
    const again = await createCapture(deps).capture('ses_01JABCDEF', { final: true });
    assert.deepEqual(again, { written: false, reason: 'unchanged' });

    // Different sessions accumulate in the spool while the app is closed.
    await createCapture({
      ...deps,
      sessions: { get: async () => ({ ...SESSION, id: 'ses_SECOND' }), context: async () => MESSAGES },
    }).capture('ses_SECOND', { final: false });
    assert.equal((await readdir(spoolDir)).length, 2);
  } finally {
    await rm(spoolDir, { recursive: true, force: true });
  }
});
