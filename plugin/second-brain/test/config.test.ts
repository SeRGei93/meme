import assert from 'node:assert/strict';
import { mkdtemp, rm, writeFile, mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { loadConfig, DEFAULT_IDLE_DEBOUNCE_MS } from '../config.ts';

async function withConfigFile(contents: string, fn: (paths: { home: string; env: NodeJS.ProcessEnv }) => Promise<void>) {
  const home = await mkdtemp(join(tmpdir(), 'sb-config-'));
  const dir = join(home, '.config', 'second-brain');
  await mkdir(dir, { recursive: true });
  await writeFile(join(dir, 'config.json'), contents);
  try {
    await fn({ home, env: {} });
  } finally {
    await rm(home, { recursive: true, force: true });
  }
}

test('missing config file falls back to defaults under ~/.config/second-brain', async () => {
  const home = await mkdtemp(join(tmpdir(), 'sb-config-'));
  try {
    const config = await loadConfig({ home, env: {} });
    assert.equal(config.configDir, join(home, '.config', 'second-brain'));
    assert.equal(config.spoolDir, join(home, '.config', 'second-brain', 'spool'));
    assert.equal(config.idleDebounceMs, DEFAULT_IDLE_DEBOUNCE_MS);
    assert.ok(DEFAULT_IDLE_DEBOUNCE_MS >= 3 * 60_000 && DEFAULT_IDLE_DEBOUNCE_MS <= 5 * 60_000);
  } finally {
    await rm(home, { recursive: true, force: true });
  }
});

test('partial config overrides spool_dir and idle_debounce_ms', async () => {
  await withConfigFile(JSON.stringify({ spool_dir: '/tmp/sb-spool', idle_debounce_ms: 180_000 }), async ({ home, env }) => {
    const config = await loadConfig({ home, env });
    assert.equal(config.spoolDir, '/tmp/sb-spool');
    assert.equal(config.idleDebounceMs, 180_000);
  });
});

test('server_url is read for the /remember command', async () => {
  await withConfigFile(JSON.stringify({ server_url: ' https://brain.example.test ' }), async ({ home, env }) => {
    const config = await loadConfig({ home, env });
    assert.equal(config.serverURL, 'https://brain.example.test');
  });
});

test('an empty or non-string server_url is ignored', async () => {
  for (const value of ['', '   ', 42]) {
    await withConfigFile(JSON.stringify({ server_url: value }), async ({ home, env }) => {
      assert.equal((await loadConfig({ home, env })).serverURL, undefined);
    });
  }
});

test('malformed or non-object config falls back to defaults instead of throwing', async () => {
  for (const contents of ['{not json', '[]', '"string"', 'null']) {
    await withConfigFile(contents, async ({ home, env }) => {
      const config = await loadConfig({ home, env });
      assert.equal(config.spoolDir, join(home, '.config', 'second-brain', 'spool'));
      assert.equal(config.idleDebounceMs, DEFAULT_IDLE_DEBOUNCE_MS);
    });
  }
});

test('invalid numeric values are ignored', async () => {
  await withConfigFile(
    JSON.stringify({ idle_debounce_ms: -1, max_message_chars: 'huge', max_messages: 1.5 }),
    async ({ home, env }) => {
      const config = await loadConfig({ home, env });
      assert.equal(config.idleDebounceMs, DEFAULT_IDLE_DEBOUNCE_MS);
      assert.equal(config.maxMessageChars, 200_000);
      assert.equal(config.maxMessages, 10_000);
    },
  );
});

test('idle debounce stays inside the 3–5 minute window', async () => {
  await withConfigFile(JSON.stringify({ idle_debounce_ms: 60_000 }), async ({ home, env }) => {
    assert.equal((await loadConfig({ home, env })).idleDebounceMs, 180_000);
  });
  await withConfigFile(JSON.stringify({ idle_debounce_ms: 999_999 }), async ({ home, env }) => {
    assert.equal((await loadConfig({ home, env })).idleDebounceMs, 300_000);
  });
});

test('clamps contract limits instead of trusting a larger config value', async () => {
  await withConfigFile(
    JSON.stringify({
      max_message_chars: 999_999,
      max_messages: 99_999,
      max_tool_calls_per_message: 9_999,
      max_tool_field_chars: 9_999,
    }),
    async ({ home, env }) => {
      const config = await loadConfig({ home, env });
      assert.equal(config.maxMessageChars, 200_000);
      assert.equal(config.maxMessages, 10_000);
      assert.equal(config.maxToolCallsPerMessage, 500);
      assert.equal(config.maxToolFieldChars, 2_000);
    },
  );
});

test('spool_dir expands a leading tilde', async () => {
  await withConfigFile(JSON.stringify({ spool_dir: '~/sb-spool' }), async ({ home, env }) => {
    const config = await loadConfig({ home, env });
    assert.equal(config.spoolDir, join(home, 'sb-spool'));
  });
});

test('XDG_CONFIG_HOME replaces ~/.config', async () => {
  const home = await mkdtemp(join(tmpdir(), 'sb-config-'));
  const xdg = await mkdtemp(join(tmpdir(), 'sb-xdg-'));
  try {
    const dir = join(xdg, 'second-brain');
    await mkdir(dir, { recursive: true });
    await writeFile(join(dir, 'config.json'), JSON.stringify({ idle_debounce_ms: 300_000 }));
    const config = await loadConfig({ home, env: { XDG_CONFIG_HOME: xdg } });
    assert.equal(config.configDir, dir);
    assert.equal(config.spoolDir, join(dir, 'spool'));
    assert.equal(config.idleDebounceMs, 300_000);
  } finally {
    await rm(home, { recursive: true, force: true });
    await rm(xdg, { recursive: true, force: true });
  }
});
