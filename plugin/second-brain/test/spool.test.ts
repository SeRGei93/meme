import assert from 'node:assert/strict';
import { mkdtemp, readdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { writeBundle, type SpoolFilesystem } from '../spool.ts';
import type { SpoolBundle } from '../types.ts';

function bundle(overrides: Partial<SpoolBundle> = {}): SpoolBundle {
  return {
    schema_version: 1,
    bundle_id: '6f1a2f4a-5b1c-4c55-9dbb-59f8c0f1a2b3',
    source: 'opencode',
    external_id: 'ses_01JABCDEF',
    project_key: 'github.com/SeRGei93/meme',
    project_path: '/repo',
    title: 'Fix migration ordering',
    final: false,
    started_at: '2026-11-03T12:00:00.000Z',
    updated_at: '2026-11-03T12:30:00.000Z',
    messages: [{ id: 'msg_1', role: 'user', text: 'hello', at: '2026-11-03T12:00:10.000Z' }],
    metadata: { branch: 'main' },
    ...overrides,
  };
}

test('writes a readable bundle with the timestamp-session-uuid filename', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'sb-spool-'));
  try {
    const path = await writeBundle({
      dir,
      bundle: bundle(),
      now: new Date('2026-11-03T12:34:56.789Z'),
      uuid: '9a7c2f5e-1111-4222-8333-444455556666',
    });
    assert.equal(path, join(dir, '20261103T123456Z-ses_01JABCDEF-9a7c2f5e-1111-4222-8333-444455556666.json'));
    const written = JSON.parse(await readFile(path, 'utf8'));
    assert.deepEqual(written, bundle());
    assert.deepEqual(await readdir(dir), ['20261103T123456Z-ses_01JABCDEF-9a7c2f5e-1111-4222-8333-444455556666.json']);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test('creates the spool directory and removes the temporary file after rename', async () => {
  const root = await mkdtemp(join(tmpdir(), 'sb-spool-'));
  const dir = join(root, 'nested', 'spool');
  try {
    const path = await writeBundle({ dir, bundle: bundle(), now: new Date(0), uuid: 'uuid-1' });
    assert.equal(path.startsWith(dir), true);
    const entries = await readdir(dir);
    assert.equal(entries.length, 1);
    assert.equal(entries.some((entry) => entry.endsWith('.tmp')), false);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('sanitizes unsafe characters in the external id', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'sb-spool-'));
  try {
    const path = await writeBundle({
      dir,
      bundle: bundle({ external_id: 'ses/with spaces:and*stars' }),
      now: new Date(0),
      uuid: 'uuid-1',
    });
    const name = path.slice(path.lastIndexOf('/') + 1);
    assert.equal(name.includes('/'), false);
    assert.match(name, /^19700101T000000Z-ses_with_spaces_and_stars-uuid-1\.json$/);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test('cleans up the temporary file and rethrows when the rename fails', async () => {
  const removed: string[] = [];
  const fs: SpoolFilesystem = {
    mkdir: async () => undefined,
    writeFile: async () => undefined,
    rename: async () => {
      throw new Error('disk full');
    },
    rm: async (path) => {
      removed.push(path);
    },
  };

  await assert.rejects(
    writeBundle({ dir: '/spool', bundle: bundle(), now: new Date(0), uuid: 'uuid-1', fs }),
    /disk full/,
  );
  assert.deepEqual(removed, ['/spool/19700101T000000Z-ses_01JABCDEF-uuid-1.json.tmp']);
});

test('keeps two captures of the same session as separate files', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'sb-spool-'));
  try {
    const first = await writeBundle({ dir, bundle: bundle({ final: false }), now: new Date(1000), uuid: 'uuid-1' });
    const second = await writeBundle({ dir, bundle: bundle({ final: true }), now: new Date(2000), uuid: 'uuid-2' });
    assert.notEqual(first, second);
    assert.equal((await readdir(dir)).length, 2);
    assert.equal(JSON.parse(await readFile(second, 'utf8')).final, true);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
