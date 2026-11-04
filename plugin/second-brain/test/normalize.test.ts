import assert from 'node:assert/strict';
import { test } from 'node:test';
import { normalizeSession } from '../normalize.ts';
import type { NormalizeInput, SessionMessageLike } from '../types.ts';

const SESSION = {
  id: 'ses_01JABCDEF',
  title: 'Fix migration ordering',
  agent: 'build',
  model: { providerID: 'anthropic', id: 'claude-sonnet' },
  time: { created: Date.parse('2026-11-03T12:00:00Z'), updated: Date.parse('2026-11-03T12:30:00Z') },
  location: { directory: '/repo' },
};

function input(messages: SessionMessageLike[], overrides: Partial<NormalizeInput> = {}): NormalizeInput {
  return {
    bundleId: '6f1a2f4a-5b1c-4c55-9dbb-59f8c0f1a2b3',
    session: SESSION,
    messages,
    final: false,
    projectKey: 'github.com/SeRGei93/meme',
    projectPath: '/repo',
    branch: 'main',
    opencodeVersion: '2.2.5',
    ...overrides,
  };
}

test('maps user and assistant text to a spool bundle', () => {
  const bundle = normalizeSession(
    input([
      { id: 'msg_1', type: 'user', time: { created: Date.parse('2026-11-03T12:00:10Z') }, text: 'Why 004 first?' },
      {
        id: 'msg_2',
        type: 'assistant',
        time: { created: Date.parse('2026-11-03T12:00:20Z'), completed: Date.parse('2026-11-03T12:00:30Z') },
        content: [
          { type: 'reasoning', text: 'hidden chain of thought' },
          { type: 'text', text: 'Filenames sort lexicographically.' },
          {
            type: 'tool',
            name: 'bash',
            state: { status: 'completed', input: { command: 'go test ./...' } },
          },
          {
            type: 'tool',
            name: 'read',
            state: { status: 'completed', input: { filePath: '/repo/server/main.go' } },
          },
        ],
      },
    ]),
  );

  assert.ok(bundle);
  assert.equal(bundle.schema_version, 1);
  assert.equal(bundle.source, 'opencode');
  assert.equal(bundle.external_id, 'ses_01JABCDEF');
  assert.equal(bundle.project_key, 'github.com/SeRGei93/meme');
  assert.equal(bundle.project_path, '/repo');
  assert.equal(bundle.final, false);
  assert.equal(bundle.started_at, '2026-11-03T12:00:00.000Z');
  assert.equal(bundle.updated_at, '2026-11-03T12:00:30.000Z');
  assert.deepEqual(bundle.metadata, {
    agent: 'build',
    model: 'anthropic/claude-sonnet',
    branch: 'main',
    opencode_version: '2.2.5',
  });
  assert.deepEqual(bundle.messages, [
    { id: 'msg_1', role: 'user', text: 'Why 004 first?', at: '2026-11-03T12:00:10.000Z' },
    {
      id: 'msg_2',
      role: 'assistant',
      text: 'Filenames sort lexicographically.',
      at: '2026-11-03T12:00:30.000Z',
      tool_calls: [
        { name: 'bash', command: 'go test ./...' },
        { name: 'read', path: '/repo/server/main.go' },
      ],
    },
  ]);
});

test('drops reasoning-only and empty messages; returns undefined when nothing is usable', () => {
  const reasoningOnly = normalizeSession(
    input([
      { id: 'msg_1', type: 'assistant', time: { created: 1 }, content: [{ type: 'reasoning', text: 'x' }] },
    ]),
  );
  assert.equal(reasoningOnly, undefined);

  const systemOnly = normalizeSession(input([{ id: 'msg_1', type: 'system', time: { created: 1 }, text: 'boot' }]));
  assert.equal(systemOnly, undefined);
});

test('tool-only assistant message is kept with empty text', () => {
  const bundle = normalizeSession(
    input([
      {
        id: 'msg_1',
        type: 'assistant',
        time: { created: 1 },
        content: [{ type: 'tool', name: 'bash', state: { status: 'completed', input: { command: 'ls' } } }],
      },
    ]),
  );
  assert.ok(bundle);
  assert.equal(bundle.messages[0]?.text, '');
  assert.deepEqual(bundle.messages[0]?.tool_calls, [{ name: 'bash', command: 'ls' }]);
});

test('truncates long text and tool fields to schema limits', () => {
  const bundle = normalizeSession(
    input(
      [
        {
          id: 'msg_1',
          type: 'assistant',
          time: { created: 1 },
          content: [
            { type: 'text', text: 'a'.repeat(50) },
            { type: 'tool', name: 'bash', state: { input: { command: 'b'.repeat(50), path: 'p'.repeat(50) } } },
          ],
        },
      ],
      { limits: { maxMessageChars: 10, maxToolCallsPerMessage: 500, maxToolFieldChars: 5, maxMessages: 10_000 } },
    ),
  );
  assert.ok(bundle);
  assert.equal(bundle.messages[0]?.text, 'aaaaaaaaaa');
  assert.deepEqual(bundle.messages[0]?.tool_calls, [{ name: 'bash', command: 'bbbbb', path: 'ppppp' }]);
});

test('keeps the most recent messages when the bundle exceeds maxMessages', () => {
  const messages: SessionMessageLike[] = Array.from({ length: 5 }, (_, i) => ({
    id: `msg_${i}`,
    type: 'user',
    time: { created: i * 1000 },
    text: `m${i}`,
  }));
  const bundle = normalizeSession(
    input(messages, { limits: { maxMessages: 2, maxMessageChars: 100, maxToolCallsPerMessage: 10, maxToolFieldChars: 100 } }),
  );
  assert.ok(bundle);
  assert.deepEqual(
    bundle.messages.map((m) => m.id),
    ['msg_3', 'msg_4'],
  );
});

test('caps project, metadata and message fields to the schema limits', () => {
  const bundle = normalizeSession(
    input(
      [
        {
          id: 'm'.repeat(250),
          type: 'user',
          time: { created: 1 },
          text: 'hello',
        },
      ],
      {
        session: {
          ...SESSION,
          id: 's'.repeat(250),
          agent: 'a'.repeat(120),
          model: { providerID: 'p'.repeat(120), id: 'm'.repeat(120) },
        },
        projectKey: 'k'.repeat(600),
        projectPath: `/${'p'.repeat(1_100)}`,
        branch: 'b'.repeat(220),
        opencodeVersion: 'v'.repeat(120),
      },
    ),
  );
  assert.ok(bundle);
  assert.equal(bundle.external_id.length, 200);
  assert.equal(bundle.project_key.length, 500);
  assert.equal(bundle.project_path.length, 1_000);
  assert.equal(bundle.metadata.agent?.length, 100);
  assert.equal(bundle.metadata.model?.length, 200);
  assert.equal(bundle.metadata.branch?.length, 200);
  assert.equal(bundle.metadata.opencode_version?.length, 100);
  assert.equal(bundle.messages[0]?.id.length, 200);
});

test('a missing assistant timestamp falls back to the session start deterministically', () => {
  const build = () =>
    normalizeSession(
      input([
        {
          id: 'msg_1',
          type: 'assistant',
          content: [{ type: 'tool', name: 'bash', state: { input: { command: 'ls' } } }],
        },
      ]),
    );
  const first = build();
  const second = build();
  assert.equal(first?.messages[0]?.at, '2026-11-03T12:00:00.000Z');
  assert.deepEqual(first, second);
});

test('derives a title from the first user line when the session has none', () => {
  const bundle = normalizeSession(
    input([{ id: 'msg_1', type: 'user', time: { created: 1 }, text: '  First line\nsecond line  ' }], {
      session: { ...SESSION, title: undefined },
    }),
  );
  assert.ok(bundle);
  assert.equal(bundle.title, 'First line');
});

test('falls back to the session id for the title', () => {
  const bundle = normalizeSession(
    input([{ id: 'msg_1', type: 'user', time: { created: 1 }, text: '   ' }], {
      session: { ...SESSION, title: '' },
    }),
  );
  assert.equal(bundle, undefined);

  const toolOnly = normalizeSession(
    input([{ id: 'msg_1', type: 'assistant', time: { created: 1 }, content: [{ type: 'tool', name: 'bash' }] }], {
      session: { ...SESSION, title: undefined },
    }),
  );
  assert.ok(toolOnly);
  assert.equal(toolOnly.title, 'Session ses_01JABCDEF');
});
