import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';

const ROOT = fileURLToPath(new URL('../..', import.meta.url));
const SCHEMA_PATH = join(ROOT, 'docs/spool.schema.json');
const FIXTURE_PATH = join(ROOT, 'tools/fixtures/spool-bundle.example.json');
const CLI_PATH = join(ROOT, 'tools/validate-spool.mjs');

async function loadSchema() {
  return JSON.parse(await readFile(SCHEMA_PATH, 'utf8'));
}

async function loadFixture() {
  return JSON.parse(await readFile(FIXTURE_PATH, 'utf8'));
}

function compile(schema) {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  return ajv.compile(schema);
}

function validBundle(overrides = {}) {
  return {
    schema_version: 1,
    bundle_id: '6f1a2f4a-5b1c-4c55-9dbb-59f8c0f1a2b3',
    source: 'opencode',
    external_id: 'ses_01JABCDEF',
    project_key: 'github.com/SeRGei93/meme',
    project_path: '/Users/serg/PhpstormProjects/meme',
    title: 'Fix SQLite migration ordering',
    final: true,
    started_at: '2026-11-03T12:00:00Z',
    updated_at: '2026-11-03T13:00:00Z',
    messages: [
      { id: 'msg_1', role: 'user', text: 'Why did migration 004 run first?', at: '2026-11-03T12:00:10Z' },
      { id: 'msg_2', role: 'assistant', text: 'Filenames sorted lexicographically.', at: '2026-11-03T12:00:30Z' },
    ],
    metadata: { agent: 'build', model: 'provider/model', branch: 'main', opencode_version: '1.2.3' },
    ...overrides,
  };
}

test('spool schema is valid JSON Schema 2020-12 and compiles strictly', async () => {
  const schema = await loadSchema();
  assert.equal(schema.$schema, 'https://json-schema.org/draft/2020-12/schema');
  assert.match(schema.$id, /spool.*v1/);
  assert.doesNotThrow(() => compile(schema));
});

test('example bundle on disk validates', async () => {
  const validate = compile(await loadSchema());
  const bundle = await loadFixture();
  assert.equal(validate(bundle), true, JSON.stringify(validate.errors, null, 2));
});

test('architecture bundle with tool calls validates', async () => {
  const validate = compile(await loadSchema());
  const bundle = validBundle({
    messages: [
      {
        id: 'msg_1',
        role: 'assistant',
        text: '',
        at: '2026-11-03T12:00:10Z',
        tool_calls: [{ name: 'bash', command: 'go test ./...', path: '/repo' }],
      },
    ],
  });
  assert.equal(validate(bundle), true, JSON.stringify(validate.errors, null, 2));
});

const INVALID_CASES = {
  'missing bundle_id': (b) => delete b.bundle_id,
  'bundle_id is not a uuid': (b) => (b.bundle_id = 'not-a-uuid'),
  'source is not opencode': (b) => (b.source = 'jarvis'),
  'schema_version is not 1': (b) => (b.schema_version = 2),
  'messages is empty': (b) => (b.messages = []),
  'message role is unknown': (b) => (b.messages[0].role = 'narrator'),
  'message is missing at': (b) => delete b.messages[0].at,
  'timestamp is not RFC3339': (b) => (b.started_at = 'yesterday'),
  'title is empty': (b) => (b.title = ''),
  'unknown top-level field': (b) => (b.extra = true),
  'unknown metadata field': (b) => (b.metadata.extra = true),
};

for (const [name, mutate] of Object.entries(INVALID_CASES)) {
  test(`rejects bundle where ${name}`, async () => {
    const validate = compile(await loadSchema());
    const bundle = validBundle();
    mutate(bundle);
    assert.equal(validate(bundle), false, `${name} should be rejected`);
  });
}

test('ingest request definition matches the wire contract without source fields', async () => {
  const schema = await loadSchema();
  const validate = compile({ ...schema, $ref: '#/$defs/IngestRequest' });
  const { schema_version, source, ...request } = validBundle();

  assert.equal(validate(request), true, JSON.stringify(validate.errors, null, 2));

  const withSource = { ...request, source: 'jarvis' };
  assert.equal(validate(withSource), false, 'ingest requests must not carry source');
});

test('CLI validates a file and exits 0', async () => {
  const result = spawnSync(process.execPath, [CLI_PATH, FIXTURE_PATH], { encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /valid/i);
});

test('CLI reports the failing path and exits 1', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'spool-'));
  try {
    const path = join(dir, 'broken.json');
    await writeFile(path, JSON.stringify(validBundle({ title: '' })));
    const result = spawnSync(process.execPath, [CLI_PATH, path], { encoding: 'utf8' });
    assert.equal(result.status, 1);
    assert.match(result.stderr, /\/title/);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test('CLI validates bundles from stdin', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'spool-'));
  try {
    const path = join(dir, 'bundle.json');
    await writeFile(path, JSON.stringify(await loadFixture()));
    const result = spawnSync(process.execPath, [CLI_PATH, '--stdin'], {
      encoding: 'utf8',
      input: await readFile(path, 'utf8'),
    });
    assert.equal(result.status, 0, result.stderr);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
