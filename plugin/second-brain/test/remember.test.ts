import assert from 'node:assert/strict';
import { test } from 'node:test';
import { defaultConfig } from '../config.ts';
import {
  remember,
  runRememberCommand,
  type RememberCommandDeps,
  type RememberOutcome,
} from '../remember.ts';

const SERVER = 'https://brain.example.test';
const NOW = new Date('2026-11-04T20:00:00Z');

interface Call {
  readonly url: string;
  readonly init: RequestInit;
}

interface Harness {
  readonly deps: RememberCommandDeps;
  readonly calls: Call[];
  readonly messages: Array<{ sessionID: string; text: string }>;
}

function harness(
  reply:
    | { status?: number; body?: unknown; delayMs?: number; reject?: Error }
    | (() => { status?: number; body?: unknown }),
  options: {
    serverURL?: string | null;
    token?: string | null;
    timeoutMs?: number;
    text?: string;
  } = {},
): Harness {
  const calls: Call[] = [];
  const messages: Harness['messages'] = [];
  const config = {
    ...defaultConfig('/config/second-brain'),
    serverURL: options.serverURL === undefined ? SERVER : (options.serverURL ?? undefined),
  };
  const token = options.token === undefined ? 'sb_opencode_token' : options.token;

  const deps: RememberCommandDeps = {
    config,
    projectKey: 'opencode:prj_1',
    now: () => NOW,
    timeoutMs: options.timeoutMs ?? 100,
    readFile: async (path) => {
      if (path.endsWith('/mcp_token')) {
        if (token === null) throw new Error('ENOENT');
        return token;
      }
      throw new Error(`unexpected read ${path}`);
    },
    fetch: (async (input: string | URL | Request, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url;
      calls.push({ url, init: init ?? {} });
      if (typeof reply === 'function') {
        return respond(reply());
      }
      if (reply.reject) throw reply.reject;
      if (reply.delayMs) {
        await new Promise((resolve, reject) => {
          const timer = setTimeout(resolve, reply.delayMs);
          init?.signal?.addEventListener('abort', () => {
            clearTimeout(timer);
            reject(new DOMException('aborted', 'AbortError'));
          });
        });
      }
      return respond(reply);
    }) as typeof fetch,
  };

  return {
    deps: {
      ...deps,
      say: async (input) => {
        messages.push(input);
      },
    },
    calls,
    messages,
  };
}

function respond(reply: { status?: number; body?: unknown }): Response {
  return new Response(JSON.stringify(reply.body ?? {}), {
    status: reply.status ?? 201,
    headers: { 'Content-Type': 'application/json' },
  });
}

function expectOk(outcome: RememberOutcome): Extract<RememberOutcome, { ok: true }> {
  assert.ok(outcome.ok, `expected success, got ${!outcome.ok ? outcome.message : ''}`);
  return outcome;
}

test('remember posts the memory with the OpenCode token', async () => {
  const h = harness({
    status: 201,
    body: { record_id: 43, job_id: null, status: 'indexed', replayed: false },
  });

  const outcome = expectOk(await remember('  Backup bucket is meme  ', h.deps));

  assert.equal(outcome.recordID, 43);
  assert.equal(outcome.status, 'indexed');
  assert.equal(outcome.replayed, false);

  const [call] = h.calls;
  assert.ok(call);
  assert.equal(call.url, `${SERVER}/api/v1/memories`);
  assert.equal(call.init.method, 'POST');
  const headers = new Headers(call.init.headers);
  assert.equal(headers.get('Authorization'), 'Bearer sb_opencode_token');
  assert.equal(headers.get('Content-Type'), 'application/json');

  const body = JSON.parse(String(call.init.body));
  assert.deepEqual(body, {
    raw_text: 'Backup bucket is meme',
    project_key: 'opencode:prj_1',
    occurred_at: NOW.toISOString(),
  });
});

test('remember accepts a queued card and a replayed response', async () => {
  const queued = harness({
    status: 201,
    body: { record_id: 7, job_id: 9, status: 'queued', replayed: false },
  });
  const replay = harness({
    status: 200,
    body: { record_id: 7, job_id: null, status: 'indexed', replayed: true },
  });

  assert.equal(expectOk(await remember('fact', queued.deps)).status, 'queued');
  assert.equal(expectOk(await remember('fact', replay.deps)).replayed, true);
});

test('an empty command text is rejected locally', async () => {
  const h = harness({ status: 201 });

  const outcome = await remember('   ', h.deps);

  assert.equal(outcome.ok, false);
  assert.match(!outcome.ok ? outcome.message : '', /текст/i);
  assert.equal(h.calls.length, 0);
});

test('a missing server or token explains how to fix setup', async () => {
  const noServer = harness({ status: 201 }, { serverURL: null });
  const noToken = harness({ status: 201 }, { token: null });

  const serverOutcome = await remember('fact', noServer.deps);
  const tokenOutcome = await remember('fact', noToken.deps);

  assert.match(!serverOutcome.ok ? serverOutcome.message : '', /SecondBrain\.app|настроен/i);
  assert.match(!tokenOutcome.ok ? tokenOutcome.message : '', /токен/i);
  assert.equal(noServer.calls.length, 0);
  assert.equal(noToken.calls.length, 0);
});

test('a rejected token is reported clearly', async () => {
  const h = harness({
    status: 401,
    body: { error: { code: 'unauthorized', message: 'token revoked' } },
  });

  const outcome = await remember('fact', h.deps);

  assert.equal(outcome.ok, false);
  assert.match(!outcome.ok ? outcome.message : '', /токен/i);
});

test('server errors include the contract code and message', async () => {
  const h = harness({
    status: 503,
    body: { error: { code: 'service_unavailable', message: 'database locked', request_id: 'req_1' } },
  });

  const outcome = await remember('fact', h.deps);

  assert.equal(outcome.ok, false);
  assert.match(!outcome.ok ? outcome.message : '', /503/);
  assert.match(!outcome.ok ? outcome.message : '', /database locked/);
});

test('a network failure does not throw and is reported', async () => {
  const h = harness({ reject: new TypeError('fetch failed') });

  const outcome = await remember('fact', h.deps);

  assert.equal(outcome.ok, false);
  assert.match(!outcome.ok ? outcome.message : '', /недоступен/i);
});

test('a slow server times out instead of blocking the session', async () => {
  const h = harness({ delayMs: 1000 }, { timeoutMs: 10 });

  const started = Date.now();
  const outcome = await remember('fact', h.deps);

  assert.equal(outcome.ok, false);
  assert.match(!outcome.ok ? outcome.message : '', /время|недоступен/i);
  assert.ok(Date.now() - started < 500);
});

test('the memories URL keeps a base path and tolerates a trailing slash', async () => {
  const h = harness({ status: 201, body: { record_id: 1, job_id: null, status: 'indexed', replayed: false } }, {
    serverURL: 'https://brain.example.test/brain/',
  });

  await remember('fact', h.deps);

  assert.equal(h.calls[0]?.url, 'https://brain.example.test/brain/api/v1/memories');
});

test('the command reports success and failure through a synthetic message', async () => {
  const ok = harness({
    status: 201,
    body: { record_id: 43, job_id: null, status: 'indexed', replayed: false },
  });
  const failed = harness({ status: 500, body: { error: { code: 'internal_error', message: 'boom' } } });

  await runRememberCommand({ sessionID: 'ses_1', prompt: { text: 'remember this' } }, ok.deps);
  await runRememberCommand({ sessionID: 'ses_1', prompt: { text: 'remember this' } }, failed.deps);

  assert.equal(ok.messages.length, 1);
  assert.equal(ok.messages[0]?.sessionID, 'ses_1');
  assert.match(ok.messages[0]?.text ?? '', /43/);
  assert.equal(failed.messages.length, 1);
  assert.match(failed.messages[0]?.text ?? '', /Не удалось/);
});

test('the command strips a leading /remember prefix', async () => {
  const h = harness({
    status: 201,
    body: { record_id: 2, job_id: null, status: 'indexed', replayed: false },
  });

  await runRememberCommand({ sessionID: 'ses_1', prompt: { text: '/remember bucket is meme' } }, h.deps);

  const body = JSON.parse(String(h.calls[0]?.init.body));
  assert.equal(body.raw_text, 'bucket is meme');
});

test('the command never rejects when the synthetic message itself fails', async () => {
  const h = harness({
    status: 201,
    body: { record_id: 1, job_id: null, status: 'indexed', replayed: false },
  });
  await assert.doesNotReject(
    runRememberCommand(
      { sessionID: 'ses_1', prompt: { text: 'fact' } },
      {
        ...h.deps,
        say: async () => {
          throw new Error('session closed');
        },
      },
    ),
  );
});
