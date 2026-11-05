import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { after, before, test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { startMockApi } from '../mock-api.mjs';

const ADMIN_TOKEN = 'test-admin-token';
const FIXTURE_PATH = fileURLToPath(new URL('../fixtures/spool-bundle.example.json', import.meta.url));

let api;
let baseUrl;

function request(path, { method = 'GET', token, body, headers = {} } = {}) {
  const requestHeaders = { ...headers };
  if (token) requestHeaders.authorization = `Bearer ${token}`;
  if (body !== undefined && !requestHeaders['content-type']) {
    requestHeaders['content-type'] = 'application/json';
  }
  return fetch(`${baseUrl}${path}`, {
    method,
    headers: requestHeaders,
    body: body === undefined ? undefined : typeof body === 'string' ? body : JSON.stringify(body),
  });
}

async function jsonRequest(path, options) {
  const response = await request(path, options);
  const text = await response.text();
  return {
    status: response.status,
    headers: response.headers,
    body: text ? JSON.parse(text) : null,
  };
}

function ingestBody(overrides = {}) {
  const { schema_version, source, ...request } = structuredClone(ingestFixture);
  return { ...request, ...overrides };
}

let ingestFixture;

before(async () => {
  ingestFixture = JSON.parse(await readFile(FIXTURE_PATH, 'utf8'));
  api = await startMockApi({ port: 0, host: '127.0.0.1', adminToken: ADMIN_TOKEN, logger: null });
  baseUrl = api.url;
});

after(async () => {
  await api.close();
});

test('mock API implements the v1 contract', async (t) => {
  const state = {};

  await t.test('health is public and reports a healthy database', async () => {
    const response = await jsonRequest('/health', { token: 'garbage-token' });
    assert.equal(response.status, 200);
    assert.equal(response.body.status, 'ok');
    assert.equal(response.body.database.status, 'ok');
    assert.equal(response.body.database.fts5, true);
  });

  await t.test('protected routes reject missing and unknown tokens with 401', async () => {
    for (const token of [undefined, 'sb_ui_unknown']) {
      const response = await jsonRequest('/api/v1/search?q=sqlite', { token });
      assert.equal(response.status, 401);
      assert.equal(response.body.error.code, 'unauthorized');
      assert.equal(typeof response.body.error.request_id, 'string');
    }
  });

  await t.test('admin can create a UI client and token is only returned once', async () => {
    const response = await jsonRequest('/api/v1/clients', {
      method: 'POST',
      token: ADMIN_TOKEN,
      body: { name: 'second-brain-app', kind: 'ui', scopes: ['ui'] },
    });
    assert.equal(response.status, 201);
    assert.match(response.body.token, /^sb_ui_[A-Za-z0-9_-]{43}$/);
    assert.equal(response.body.client.kind, 'ui');
    assert.equal(response.body.client.source, null);
    assert.deepEqual(response.body.client.scopes, ['ui']);
    assert.equal(response.body.client.revoked_at, null);
    state.uiToken = response.body.token;
    state.uiClientId = response.body.client.id;

    const list = await jsonRequest('/api/v1/clients', { token: ADMIN_TOKEN });
    const stored = list.body.items.find((client) => client.id === state.uiClientId);
    assert.ok(stored);
    assert.equal('token' in stored, false);
  });

  await t.test('UI scope cannot create clients or read the queue', async () => {
    for (const [path, method, body] of [
      ['/api/v1/clients', 'POST', { name: 'nope', kind: 'ui', scopes: ['ui'] }],
      ['/api/v1/jobs', 'GET', undefined],
    ]) {
      const response = await jsonRequest(path, { method, token: state.uiToken, body });
      assert.equal(response.status, 403);
      assert.equal(response.body.error.code, 'forbidden');
      assert.deepEqual(response.body.error.details.required_any_of, ['admin']);
    }
  });

  await t.test('admin creates ingest clients with server-derived or explicit source', async () => {
    state.opencode = (await jsonRequest('/api/v1/clients', {
      method: 'POST',
      token: ADMIN_TOKEN,
      body: { name: 'opencode-macbook', kind: 'opencode', scopes: ['ingest'] },
    })).body;

    state.jarvis = (await jsonRequest('/api/v1/clients', {
      method: 'POST',
      token: ADMIN_TOKEN,
      body: { name: 'jarvis', kind: 'agent', source: 'jarvis', scopes: ['ingest', 'mcp:read', 'mcp:write'] },
    })).body;

    assert.equal(state.opencode.client.kind, 'agent');
    assert.equal(state.opencode.client.source, 'opencode-macbook');
    assert.equal(state.jarvis.client.kind, 'agent');
    assert.equal(state.jarvis.client.source, 'jarvis');
    assert.match(state.opencode.token, /^sb_agent_[A-Za-z0-9_-]{43}$/);
    assert.match(state.jarvis.token, /^sb_agent_[A-Za-z0-9_-]{43}$/);

    const duplicate = await jsonRequest('/api/v1/clients', {
      method: 'POST',
      token: ADMIN_TOKEN,
      body: { name: 'another-agent', kind: 'agent', source: 'jarvis', scopes: ['mcp:read'] },
    });
    assert.equal(duplicate.status, 409);
    assert.equal(duplicate.body.error.details.field, 'source');
  });

  await t.test('duplicate client names conflict and invalid scopes fail validation', async () => {
    const duplicate = await jsonRequest('/api/v1/clients', {
      method: 'POST',
      token: ADMIN_TOKEN,
      body: { name: 'jarvis', kind: 'agent', source: 'other', scopes: ['ingest'] },
    });
    assert.equal(duplicate.status, 409);
    assert.equal(duplicate.body.error.code, 'conflict');

    const invalid = await jsonRequest('/api/v1/clients', {
      method: 'POST',
      token: ADMIN_TOKEN,
      body: { name: 'root-client', kind: 'ui', scopes: ['root'] },
    });
    assert.equal(invalid.status, 422);
    assert.equal(invalid.body.error.code, 'validation_failed');
  });

  await t.test('ingest requires a source-bearing client and a valid bundle', async () => {
    const unauthorized = await jsonRequest('/api/v1/ingest', { method: 'POST', body: ingestBody() });
    assert.equal(unauthorized.status, 401);

    const wrongScope = await jsonRequest('/api/v1/ingest', {
      method: 'POST',
      token: state.uiToken,
      body: ingestBody(),
    });
    assert.equal(wrongScope.status, 403);

    const noSource = (await jsonRequest('/api/v1/clients', {
      method: 'POST',
      token: ADMIN_TOKEN,
      body: { name: 'ui-ingest', kind: 'ui', scopes: ['ingest'] },
    })).body;
    const wrongKind = await jsonRequest('/api/v1/ingest', {
      method: 'POST',
      token: noSource.token,
      body: ingestBody(),
    });
    assert.equal(wrongKind.status, 403);

    const smuggledSource = await jsonRequest('/api/v1/ingest', {
      method: 'POST',
      token: state.opencode.token,
      body: { ...ingestBody(), source: 'jarvis' },
    });
    assert.equal(smuggledSource.status, 422);
    assert.equal(smuggledSource.body.error.code, 'validation_failed');

    const wrongType = await jsonRequest('/api/v1/ingest', {
      method: 'POST',
      token: state.opencode.token,
      body: 'not-json',
      headers: { 'content-type': 'text/plain' },
    });
    assert.equal(wrongType.status, 415);
    assert.equal(wrongType.body.error.code, 'unsupported_media_type');
  });

  await t.test('ingest stores a final session, queues a card and replays idempotently', async () => {
    const first = await jsonRequest('/api/v1/ingest', {
      method: 'POST',
      token: state.opencode.token,
      headers: { 'idempotency-key': ingestFixture.bundle_id },
      body: ingestBody(),
    });
    assert.equal(first.status, 202);
    assert.equal(first.body.status, 'queued');
    assert.equal(first.body.replayed, false);
    assert.equal(typeof first.body.record_id, 'number');
    assert.equal(typeof first.body.job_id, 'number');
    state.recordId = first.body.record_id;
    state.jobId = first.body.job_id;

    const replay = await jsonRequest('/api/v1/ingest', {
      method: 'POST',
      token: state.opencode.token,
      headers: { 'idempotency-key': ingestFixture.bundle_id },
      body: ingestBody(),
    });
    assert.equal(replay.status, 200);
    assert.equal(replay.body.replayed, true);
    assert.equal(replay.body.record_id, state.recordId);
    assert.equal(replay.body.job_id, state.jobId);

    const reused = await jsonRequest('/api/v1/ingest', {
      method: 'POST',
      token: state.opencode.token,
      headers: { 'idempotency-key': ingestFixture.bundle_id },
      body: ingestBody({ title: 'A different title' }),
    });
    assert.equal(reused.status, 409);
    assert.equal(reused.body.error.code, 'idempotency_key_reused');

    const dedupedWithNewKey = await jsonRequest('/api/v1/ingest', {
      method: 'POST',
      token: state.opencode.token,
      headers: { 'idempotency-key': 'another-key' },
      body: ingestBody(),
    });
    assert.equal(dedupedWithNewKey.status, 200);
    assert.equal(dedupedWithNewKey.body.replayed, true);
    assert.equal(dedupedWithNewKey.body.job_id, state.jobId, 'no duplicate card job');
  });

  await t.test('a non-final ingest is stored without a card job', async () => {
    const response = await jsonRequest('/api/v1/ingest', {
      method: 'POST',
      token: state.opencode.token,
      body: ingestBody({
        bundle_id: '1d3a9e2c-0000-4000-8000-000000000001',
        external_id: 'ses_draft',
        final: false,
      }),
    });
    assert.equal(response.status, 202);
    assert.equal(response.body.status, 'stored');
    assert.equal(response.body.job_id, null);
  });

  await t.test('memories accept a complete card and replay by external_id', async () => {
    const payload = {
      external_id: 'evt_decision_1',
      project_key: ingestFixture.project_key,
      occurred_at: '2026-11-03T13:30:00Z',
      title: 'Choose SQLite FTS5 over pgvector',
      summary: 'v1 uses SQLite FTS5 with BM25 and freshness decay.',
      keywords: ['sqlite', 'fts5', 'decision'],
      atoms: ['No vector search in v1.'],
      raw_text: 'Decision: use SQLite FTS5 instead of pgvector.',
    };

    const created = await jsonRequest('/api/v1/memories', {
      method: 'POST',
      token: state.jarvis.token,
      body: payload,
    });
    assert.equal(created.status, 201);
    assert.equal(created.body.status, 'indexed');
    assert.equal(created.body.job_id, null);
    assert.equal(created.body.replayed, false);
    state.memoryId = created.body.record_id;

    const replay = await jsonRequest('/api/v1/memories', {
      method: 'POST',
      token: state.jarvis.token,
      body: payload,
    });
    assert.equal(replay.status, 200);
    assert.equal(replay.body.replayed, true);
    assert.equal(replay.body.record_id, state.memoryId);

    const dedupedWithNewKey = await jsonRequest('/api/v1/memories', {
      method: 'POST',
      token: state.jarvis.token,
      headers: { 'idempotency-key': 'memory-another-key' },
      body: payload,
    });
    assert.equal(dedupedWithNewKey.status, 200);
    assert.equal(dedupedWithNewKey.body.replayed, true);
    assert.equal(dedupedWithNewKey.body.record_id, state.memoryId);
    assert.equal(dedupedWithNewKey.body.job_id, null);

    const conflicting = await jsonRequest('/api/v1/memories', {
      method: 'POST',
      token: state.jarvis.token,
      headers: { 'idempotency-key': 'memory-conflict-key' },
      body: { ...payload, raw_text: 'A different decision.' },
    });
    assert.equal(conflicting.status, 409);
    assert.equal(conflicting.body.error.code, 'conflict');

    const conflictingWithoutKey = await jsonRequest('/api/v1/memories', {
      method: 'POST',
      token: state.jarvis.token,
      body: { ...payload, raw_text: 'Yet another decision.' },
    });
    assert.equal(conflictingWithoutKey.status, 409);
    assert.equal(conflictingWithoutKey.body.error.code, 'conflict');
  });

  await t.test('text-only memories queue card generation', async () => {
    const response = await jsonRequest('/api/v1/memories', {
      method: 'POST',
      token: state.jarvis.token,
      body: {
        external_id: 'evt_reminder_1',
        project_key: 'jarvis',
        raw_text: 'Remind me that the backup bucket is called meme.',
      },
    });
    assert.equal(response.status, 201);
    assert.equal(response.body.status, 'queued');
    assert.equal(typeof response.body.job_id, 'number');
  });

  await t.test('opencode may write memories and keys are scoped per operation', async () => {
    const memory = await jsonRequest('/api/v1/memories', {
      method: 'POST',
      token: state.opencode.token,
      headers: { 'idempotency-key': 'shared-operation-key' },
      body: {
        external_id: 'evt_opencode_1',
        project_key: 'jarvis',
        raw_text: 'OpenCode can write memories for /remember.',
      },
    });
    assert.equal(memory.status, 201);
    assert.equal(memory.body.status, 'queued');

    const ingestWithSameKey = await jsonRequest('/api/v1/ingest', {
      method: 'POST',
      token: state.opencode.token,
      headers: { 'idempotency-key': 'shared-operation-key' },
      body: ingestBody({
        bundle_id: '1d3a9e2c-0000-4000-8000-000000000002',
        external_id: 'ses_cross_operation',
      }),
    });
    assert.equal(ingestWithSameKey.status, 202);
    assert.equal(ingestWithSameKey.body.replayed, false);
  });

  await t.test('search returns cards without raw text and enforces scope', async () => {
    const found = await jsonRequest('/api/v1/search?q=sqlite', { token: state.uiToken });
    assert.equal(found.status, 200);
    assert.equal(found.body.query, 'sqlite');
    assert.equal(found.body.limit, 8);
    assert.ok(found.body.total >= 1);
    const hit = found.body.items.find((item) => item.id === state.recordId);
    assert.ok(hit, 'ingested session is searchable');
    assert.equal('raw_text' in hit, false);
    assert.equal(hit.card_status, 'queued');
    assert.equal(typeof hit.score, 'number');

    const mcpRead = await jsonRequest('/api/v1/search?q=sqlite', { token: state.jarvis.token });
    assert.equal(mcpRead.status, 200);

    const noRead = await jsonRequest('/api/v1/search?q=sqlite', { token: state.opencode.token });
    assert.equal(noRead.status, 403);
    assert.deepEqual(noRead.body.error.details.required_any_of, ['ui', 'mcp:read']);
  });

  await t.test('record detail returns raw text; unknown ids are 404', async () => {
    const record = await jsonRequest(`/api/v1/records/${state.recordId}`, { token: state.uiToken });
    assert.equal(record.status, 200);
    assert.equal(record.body.id, state.recordId);
    assert.match(record.body.raw_text, /migration 004/i);
    assert.equal(record.body.revision, 1);
    assert.equal(record.body.source, 'opencode-macbook');

    const missing = await jsonRequest('/api/v1/records/999999', { token: state.uiToken });
    assert.equal(missing.status, 404);
    assert.equal(missing.body.error.code, 'not_found');
  });

  await t.test('projects report record counts', async () => {
    const projects = await jsonRequest('/api/v1/projects', { token: state.uiToken });
    assert.equal(projects.status, 200);
    const project = projects.body.items.find((item) => item.key === ingestFixture.project_key);
    assert.ok(project, 'project is present');
    assert.ok(project.record_count >= 2);
  });

  await t.test('graph is UI-only and links records in the same project', async () => {
    const forbidden = await jsonRequest('/api/v1/graph', { token: state.jarvis.token });
    assert.equal(forbidden.status, 403);

    const graph = await jsonRequest('/api/v1/graph', { token: state.uiToken });
    assert.equal(graph.status, 200);
    assert.equal(graph.body.truncated, false);
    assert.ok(graph.body.nodes.some((node) => node.id === state.recordId));
    assert.ok(
      graph.body.edges.some(
        (edge) =>
          (edge.from_id === state.recordId || edge.to_id === state.recordId) &&
          edge.reason === 'project',
      ),
      'session and memory share a project edge',
    );
  });

  await t.test('graph accepts an arbitrary source filter', async () => {
    const filtered = await jsonRequest('/api/v1/graph?source=jarvis', { token: state.uiToken });
    assert.equal(filtered.status, 200);
    assert.ok(filtered.body.nodes.every((node) => node.source === 'jarvis'));

    const unknown = await jsonRequest('/api/v1/graph?source=cursor', { token: state.uiToken });
    assert.equal(unknown.status, 200);
    assert.equal(unknown.body.nodes.length, 0);

    const bad = await jsonRequest('/api/v1/graph?source=', { token: state.uiToken });
    assert.equal(bad.status, 400);
  });

  await t.test('stats aggregate records, sources and jobs', async () => {
    const stats = await jsonRequest('/api/v1/stats', { token: state.uiToken });
    assert.equal(stats.status, 200);
    assert.ok(stats.body.records.total >= 4);
    assert.ok(stats.body.records.by_source['opencode-macbook'] >= 2);
    assert.ok(stats.body.records.by_source.jarvis >= 2);
    assert.ok(stats.body.jobs.queued >= 2);
    assert.equal(stats.body.database.status, 'ok');
  });

  await t.test('jobs support filtering and opaque cursor pagination', async () => {
    const firstPage = await jsonRequest('/api/v1/jobs?limit=1', { token: ADMIN_TOKEN });
    assert.equal(firstPage.status, 200);
    assert.equal(firstPage.body.items.length, 1);
    assert.equal(typeof firstPage.body.next_cursor, 'string');

    const secondPage = await jsonRequest(
      `/api/v1/jobs?limit=1&cursor=${encodeURIComponent(firstPage.body.next_cursor)}`,
      { token: ADMIN_TOKEN },
    );
    assert.equal(secondPage.status, 200);
    assert.notEqual(secondPage.body.items[0].id, firstPage.body.items[0].id);

    const queued = await jsonRequest('/api/v1/jobs?status=queued', { token: ADMIN_TOKEN });
    assert.ok(queued.body.items.every((job) => job.status === 'queued'));
    const failed = await jsonRequest('/api/v1/jobs?status=failed', { token: ADMIN_TOKEN });
    assert.deepEqual(failed.body.items, []);
  });

  await t.test('revoking a token is immediate and idempotent', async () => {
    const revoked = await request(`/api/v1/clients/${state.opencode.client.id}`, {
      method: 'DELETE',
      token: ADMIN_TOKEN,
    });
    assert.equal(revoked.status, 204);

    const afterRevoke = await jsonRequest('/api/v1/ingest', {
      method: 'POST',
      token: state.opencode.token,
      body: ingestBody(),
    });
    assert.equal(afterRevoke.status, 401);

    const again = await request(`/api/v1/clients/${state.opencode.client.id}`, {
      method: 'DELETE',
      token: ADMIN_TOKEN,
    });
    assert.equal(again.status, 204);

    const missing = await request('/api/v1/clients/999999', {
      method: 'DELETE',
      token: ADMIN_TOKEN,
    });
    assert.equal(missing.status, 404);
  });

  await t.test('unknown routes and wrong methods use the error envelope', async () => {
    const notFound = await jsonRequest('/api/v1/nope', { token: ADMIN_TOKEN });
    assert.equal(notFound.status, 404);
    assert.equal(notFound.body.error.code, 'not_found');

    const wrongMethod = await jsonRequest('/health', { method: 'PATCH' });
    assert.equal(wrongMethod.status, 405);
    assert.equal(wrongMethod.body.error.code, 'method_not_allowed');

    const badRecordId = await jsonRequest('/api/v1/records/abc', { token: state.uiToken });
    assert.equal(badRecordId.status, 400);
    assert.equal(badRecordId.body.error.code, 'invalid_request');

    const badClientId = await jsonRequest('/api/v1/clients/abc', {
      method: 'DELETE',
      token: ADMIN_TOKEN,
    });
    assert.equal(badClientId.status, 400);
    assert.equal(badClientId.body.error.code, 'invalid_request');
  });
});


test('mock API expires idempotency keys after the configured TTL', async () => {
  const server = await startMockApi({
    port: 0,
    host: '127.0.0.1',
    adminToken: 'ttl-admin',
    idempotencyTtlMs: 10,
    logger: null,
  });

  try {
    const created = await fetch(`${server.url}/api/v1/clients`, {
      method: 'POST',
      headers: { authorization: 'Bearer ttl-admin', 'content-type': 'application/json' },
      body: JSON.stringify({
        name: 'ttl-opencode',
        kind: 'opencode',
        scopes: ['ingest'],
      }),
    });
    assert.equal(created.status, 201);
    const { token } = await created.json();

    const body = ingestBody({
      bundle_id: '1d3a9e2c-0000-4000-8000-000000000003',
      external_id: 'ses_ttl',
    });
    const first = await fetch(`${server.url}/api/v1/ingest`, {
      method: 'POST',
      headers: {
        authorization: `Bearer ${token}`,
        'content-type': 'application/json',
        'idempotency-key': 'ttl-key',
      },
      body: JSON.stringify(body),
    });
    assert.equal(first.status, 202);

    await new Promise((resolve) => setTimeout(resolve, 25));

    const afterExpiry = await fetch(`${server.url}/api/v1/ingest`, {
      method: 'POST',
      headers: {
        authorization: `Bearer ${token}`,
        'content-type': 'application/json',
        'idempotency-key': 'ttl-key',
      },
      body: JSON.stringify({ ...body, title: 'Changed after TTL' }),
    });
    assert.equal(afterExpiry.status, 202, 'expired key must not return idempotency_key_reused');
    const payload = await afterExpiry.json();
    assert.equal(payload.replayed, false);
  } finally {
    await server.close();
  }
});
