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
const TOKEN = `sb_opencode_${'A'.repeat(43)}`;

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
  const token = options.token === undefined ? TOKEN : options.token;

  const deps: RememberCommandDeps = {
    config,
    projectKey: 'opencode:prj_1',
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
  assert.equal(headers.get('Authorization'), `Bearer ${TOKEN}`);
  assert.equal(headers.get('Content-Type'), 'application/json');

  const body = JSON.parse(String(call.init.body));
  assert.deepEqual(body, {
    raw_text: 'Backup bucket is meme',
    project_key: 'opencode:prj_1',
  });
});

test('repeating the same text sends an identical body so the server can replay it', async () => {
  const h = harness({ status: 201, body: { record_id: 1, job_id: null, status: 'indexed', replayed: false } });

  await remember('same fact', h.deps);
  await remember('same fact', h.deps);

  assert.equal(h.calls.length, 2);
  assert.equal(String(h.calls[0]?.init.body), String(h.calls[1]?.init.body));
  const body = JSON.parse(String(h.calls[0]?.init.body));
  assert.equal(body.occurred_at, undefined, 'occurred_at would break content-hash idempotency');
});

test('a project key resolver uses the same rules as capture', async () => {
  const h = harness({ status: 201, body: { record_id: 1, job_id: null, status: 'indexed', replayed: false } });

  await remember('fact', { ...h.deps, projectKey: async () => 'github.com/owner/repo' });

  const body = JSON.parse(String(h.calls[0]?.init.body));
  assert.equal(body.project_key, 'github.com/owner/repo');
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
  assert.match(!outcome.ok ? outcome.message : '', /req_1/);
});

test('rate limiting includes the retry hint', async () => {
  const h = harness(
    { status: 429, body: { error: { code: 'rate_limited', message: 'slow down' } } },
  );
  h.calls.length = 0;

  const outcome = await remember('fact', {
    ...h.deps,
    fetch: async () =>
      new Response(JSON.stringify({ error: { code: 'rate_limited', message: 'slow down' } }), {
        status: 429,
        headers: { 'Content-Type': 'application/json', 'Retry-After': '7' },
      }),
  });

  assert.equal(outcome.ok, false);
  assert.match(!outcome.ok ? outcome.message : '', /7/);
});

test('a corrupt token points to onboarding instead of blaming the network', async () => {
  const h = harness({ status: 201 }, { token: 'sb_opencode_broken' });

  const outcome = await remember('fact', h.deps);

  assert.equal(outcome.ok, false);
  assert.match(!outcome.ok ? outcome.message : '', /повреждён|токен/i);
  assert.equal(h.calls.length, 0);
});

test('a server URL with query or fragment is rejected', async () => {
  const h = harness({ status: 201 }, { serverURL: 'https://brain.example.test?token=leak' });

  const outcome = await remember('fact', h.deps);

  assert.equal(outcome.ok, false);
  assert.match(!outcome.ok ? outcome.message : '', /query|fragment/i);
  assert.equal(h.calls.length, 0);
});

test('a network failure does not throw and is reported', async () => {
  const h = harness({ reject: new TypeError('fetch failed') });

  const outcome = await remember('fact', h.deps);

  assert.equal(outcome.ok, false);
  assert.match(!outcome.ok ? outcome.message : '', /недоступен/i);
});

test('a stalled response body still times out', async () => {
  const h = harness({ status: 201 });
  let aborted = false;

  const outcome = await remember('fact', {
    ...h.deps,
    timeoutMs: 10,
    fetch: async (_input, init) => {
      aborted = false;
      return new Response(
        new ReadableStream({
          start(controller) {
            init?.signal?.addEventListener('abort', () => {
              aborted = true;
              controller.error(new DOMException('aborted', 'AbortError'));
            });
          },
        }),
        { status: 201, headers: { 'Content-Type': 'application/json' } },
      );
    },
  });

  assert.equal(outcome.ok, false);
  assert.match(!outcome.ok ? outcome.message : '', /время|недоступен/i);
  assert.equal(aborted, true);
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
