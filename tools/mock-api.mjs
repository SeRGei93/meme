#!/usr/bin/env node
import { createHash, randomBytes } from 'node:crypto';
import http from 'node:http';
import { pathToFileURL } from 'node:url';
import { createSpoolValidators, formatAjvErrors } from './lib/spool-schema.mjs';

const ALL_SCOPES = ['ingest', 'mcp:read', 'mcp:write', 'ui', 'admin'];
const SCOPES = new Set(ALL_SCOPES);
const CLIENT_KINDS = new Set(['agent', 'opencode', 'jarvis', 'ui', 'admin']);
const STORED_KINDS = new Set(['agent', 'ui', 'admin']);
const LEGACY_AGENT_KINDS = new Set(['opencode', 'jarvis']);
const JOB_STATUSES = new Set(['queued', 'running', 'done', 'failed']);
const MAX_BODY_BYTES = 10 * 1024 * 1024;
const DEFAULT_ADMIN_TOKEN = 'dev-admin-token';
const JSON_TYPE = 'application/json';
const DEFAULT_PORTS = new Set(['', '80', '443']);

class ApiError extends Error {
  constructor(status, code, message, details) {
    super(message);
    this.status = status;
    this.code = code;
    this.details = details;
  }
}

const now = () => new Date().toISOString();
const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const tokenHash = (token) => sha256(token);

function stableStringify(value) {
  if (Array.isArray(value)) return `[${value.map(stableStringify).join(',')}]`;
  if (value && typeof value === 'object') {
    return `{${Object.keys(value)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${stableStringify(value[key])}`)
      .join(',')}}`;
  }
  return JSON.stringify(value);
}

function normalizeKind(kind) {
  return LEGACY_AGENT_KINDS.has(kind) ? 'agent' : kind;
}

function deriveSource(name) {
  const slug = name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 64)
    .replace(/-+$/g, '');
  return slug || 'agent';
}

function isPlainObject(value) {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function requireString(value, field, { min = 1, max = 1000 } = {}) {
  if (typeof value !== 'string' || value.length < min || value.length > max) {
    throw new ApiError(422, 'validation_failed', `${field} must be a string of ${min}..${max} characters.`, {
      field,
    });
  }
}

function requireStringArray(value, field, maxItems) {
  if (!Array.isArray(value) || value.length > maxItems) {
    throw new ApiError(422, 'validation_failed', `${field} must be an array of at most ${maxItems} strings.`, {
      field,
    });
  }
  value.forEach((item, index) => requireString(item, `${field}[${index}]`, { max: 2000 }));
}

function parseDate(value, field) {
  if (value === undefined) return null;
  if (typeof value !== 'string' || Number.isNaN(Date.parse(value))) {
    throw new ApiError(422, 'validation_failed', `${field} must be an RFC 3339 timestamp.`, { field });
  }
  return new Date(value).toISOString();
}

function validateMemory(body) {
  if (!isPlainObject(body)) {
    throw new ApiError(422, 'validation_failed', 'Body must be a JSON object.');
  }
  const allowed = new Set([
    'external_id',
    'project_key',
    'occurred_at',
    'title',
    'summary',
    'questions',
    'keywords',
    'atoms',
    'raw_text',
    'metadata',
  ]);
  for (const key of Object.keys(body)) {
    if (!allowed.has(key)) {
      throw new ApiError(422, 'validation_failed', `${key} is not an allowed field.`, { field: key });
    }
  }

  requireString(body.raw_text, 'raw_text', { max: 2000000 });
  if (body.external_id !== undefined) requireString(body.external_id, 'external_id', { max: 200 });
  if (body.project_key !== undefined) requireString(body.project_key, 'project_key', { max: 500 });
  if (body.title !== undefined) requireString(body.title, 'title', { max: 500 });
  if (body.summary !== undefined) requireString(body.summary, 'summary', { max: 5000 });
  if (body.questions !== undefined) requireStringArray(body.questions, 'questions', 50);
  if (body.keywords !== undefined) requireStringArray(body.keywords, 'keywords', 100);
  if (body.atoms !== undefined) requireStringArray(body.atoms, 'atoms', 100);
  if (body.metadata !== undefined && !isPlainObject(body.metadata)) {
    throw new ApiError(422, 'validation_failed', 'metadata must be a JSON object.', { field: 'metadata' });
  }
  parseDate(body.occurred_at, 'occurred_at');
}

function normalizeClientRequest(body) {
  if (!isPlainObject(body)) {
    throw new ApiError(422, 'validation_failed', 'Body must be a JSON object.');
  }
  const allowed = new Set(['name', 'kind', 'source', 'scopes']);
  for (const key of Object.keys(body)) {
    if (!allowed.has(key)) {
      throw new ApiError(422, 'validation_failed', `${key} is not an allowed field.`, { field: key });
    }
  }

  if (typeof body.name !== 'string' || !/^[A-Za-z0-9._:-]{1,100}$/.test(body.name)) {
    throw new ApiError(422, 'validation_failed', 'name must match ^[A-Za-z0-9._:-]{1,100}$.', {
      field: 'name',
    });
  }
  if (!CLIENT_KINDS.has(body.kind)) {
    throw new ApiError(422, 'validation_failed', `kind must be one of ${[...CLIENT_KINDS].join(', ')}.`, {
      field: 'kind',
    });
  }
  const kind = normalizeKind(body.kind);
  let source = body.source === '' ? null : (body.source ?? null);
  if (kind === 'agent') {
    source = source ?? deriveSource(body.name);
    if (typeof source !== 'string' || !/^[a-z0-9][a-z0-9._-]{0,63}$/.test(source)) {
      throw new ApiError(422, 'validation_failed', 'source must be a lowercase slug of 1..64 characters.', {
        field: 'source',
      });
    }
  } else if (source !== null && source !== '') {
    throw new ApiError(422, 'validation_failed', `source must be null for kind ${kind}.`, {
      field: 'source',
    });
  } else {
    source = null;
  }
  if (
    !Array.isArray(body.scopes) ||
    body.scopes.length < 1 ||
    body.scopes.length > 5 ||
    new Set(body.scopes).size !== body.scopes.length ||
    body.scopes.some((scope) => !SCOPES.has(scope))
  ) {
    throw new ApiError(422, 'validation_failed', 'scopes must be a non-empty unique subset of the v1 scopes.', {
      field: 'scopes',
    });
  }

  return { name: body.name, kind, source, scopes: [...body.scopes] };
}

function cardFields(record) {
  return {
    id: record.id,
    source: record.source,
    kind: record.kind,
    project_key: record.project_key,
    title: record.title,
    summary: record.summary,
    questions: record.questions,
    keywords: record.keywords,
    atoms: record.atoms,
    is_final: record.is_final,
    started_at: record.started_at,
    updated_at: record.updated_at,
    finalized_at: record.finalized_at,
    card_status: record.card_status,
    card_model: record.card_model,
  };
}

function recordFields(record) {
  return {
    ...cardFields(record),
    raw_text: record.raw_text,
    revision: record.revision,
    metadata: record.metadata,
    indexed_at: record.indexed_at,
  };
}

function clientFields(client) {
  return {
    id: client.id,
    name: client.name,
    kind: client.kind,
    scopes: client.scopes,
    source: client.source,
    created_at: client.created_at,
    last_seen_at: client.last_seen_at,
    revoked_at: client.revoked_at,
  };
}

function rawTextFromMessages(messages) {
  return messages
    .map((message) => {
      const tools = (message.tool_calls ?? [])
        .map((tool) => {
          const target = [tool.command, tool.path].filter(Boolean).join(' → ');
          return `[tool ${tool.name}${target ? `: ${target}` : ''}]`;
        })
        .join('\n');
      return [`${message.role}: ${message.text}`, tools].filter(Boolean).join('\n');
    })
    .join('\n\n');
}

function buildRecord({
  id,
  client,
  contentHash,
  body,
  nowIso,
}) {
  return {
    id,
    client_id: client.id,
    source: client.source,
    external_id: body.external_id,
    project_key: body.project_key,
    kind: body.kind,
    title: body.title,
    summary: body.summary,
    questions: body.questions ?? [],
    keywords: body.keywords ?? [],
    atoms: body.atoms ?? [],
    raw_text: body.raw_text,
    content_hash: contentHash,
    revision: 1,
    is_final: body.is_final,
    finalized_at: body.is_final ? nowIso : null,
    started_at: body.started_at,
    updated_at: body.updated_at,
    indexed_at: nowIso,
    card_model: null,
    card_status: body.card_status,
    metadata: body.metadata ?? {},
  };
}

export async function startMockApi({
  port = 8080,
  host = '127.0.0.1',
  adminToken = DEFAULT_ADMIN_TOKEN,
  idempotencyTtlMs = 24 * 60 * 60 * 1000,
  logger = console,
} = {}) {
  const { validateIngest } = await createSpoolValidators();
  const log = logger ? (...args) => logger.log(...args) : () => {};

  const clients = [];
  const tokens = new Map();
  const records = [];
  const recordsById = new Map();
  const recordsByKey = new Map();
  const projects = new Map();
  const jobs = [];
  const idempotency = new Map();
  const counters = { client: 0, record: 0, job: 0 };

  const adminPrincipal = {
    id: 0,
    name: 'bootstrap-admin',
    kind: 'admin',
    scopes: new Set(ALL_SCOPES),
    source: null,
    bootstrap: true,
  };

  const nextId = (name) => {
    counters[name] += 1;
    return counters[name];
  };

  const touchProject = (key, path) => {
    let project = projects.get(key);
    if (!project) {
      project = {
        key,
        name: key.split(/[/:]/).filter(Boolean).at(-1) ?? key,
        path: path ?? '',
        created_at: now(),
      };
      projects.set(key, project);
    } else if (path && !project.path) {
      project.path = path;
    }
    return project;
  };

  const enqueueJob = ({ client, kind, externalId, recordId }) => {
    const timestamp = now();
    const job = {
      id: nextId('job'),
      client_id: client.id,
      kind,
      status: 'queued',
      attempts: 0,
      external_id: externalId,
      record_id: recordId,
      next_retry_at: null,
      error: null,
      created_at: timestamp,
      updated_at: timestamp,
    };
    jobs.push(job);
    return job;
  };

  const authPrincipal = (request) => {
    const header = request.headers.authorization;
    if (typeof header !== 'string') return null;
    const match = /^Bearer (.+)$/.exec(header);
    if (!match) return null;
    const token = match[1];
    if (token === adminToken) return adminPrincipal;

    const client = tokens.get(tokenHash(token));
    if (!client || client.revoked_at) return null;
    client.last_seen_at = now();
    return {
      id: client.id,
      name: client.name,
      kind: client.kind,
      scopes: new Set(client.scopes),
      source: client.source,
      bootstrap: false,
      client,
    };
  };

  const requireScope = (principal, scopes) => {
    if (scopes.length === 0) return;
    if (!scopes.some((scope) => principal.scopes.has(scope))) {
      throw new ApiError(403, 'forbidden', 'Token lacks the required scope.', {
        required_any_of: scopes,
      });
    }
  };

  const readJson = async (request) => {
    const contentType = String(request.headers['content-type'] ?? '');
    if (!contentType.toLowerCase().startsWith(JSON_TYPE)) {
      throw new ApiError(415, 'unsupported_media_type', 'Content-Type must be application/json.');
    }

    const declared = Number(request.headers['content-length'] ?? 0);
    if (declared > MAX_BODY_BYTES) {
      throw new ApiError(413, 'payload_too_large', `Request body exceeds ${MAX_BODY_BYTES} bytes.`);
    }

    const chunks = [];
    let size = 0;
    for await (const chunk of request) {
      size += chunk.length;
      if (size > MAX_BODY_BYTES) {
        throw new ApiError(413, 'payload_too_large', `Request body exceeds ${MAX_BODY_BYTES} bytes.`);
      }
      chunks.push(chunk);
    }

    const text = Buffer.concat(chunks).toString('utf8');
    if (!text) {
      throw new ApiError(400, 'invalid_request', 'Request body is required.');
    }
    try {
      return JSON.parse(text);
    } catch {
      throw new ApiError(400, 'invalid_request', 'Request body is not valid JSON.');
    }
  };

  const parseLimit = (url, { defaultValue, min = 1, max = 200 }) => {
    const raw = url.searchParams.get('limit');
    if (raw === null) return defaultValue;
    const value = Number(raw);
    if (!Number.isInteger(value) || value < min || value > max) {
      throw new ApiError(400, 'invalid_request', `limit must be an integer from ${min} to ${max}.`, {
        field: 'limit',
      });
    }
    return value;
  };

  const parseDateFilter = (url, name) => {
    const raw = url.searchParams.get(name);
    if (raw === null) return null;
    if (Number.isNaN(Date.parse(raw))) {
      throw new ApiError(400, 'invalid_request', `${name} must be an RFC 3339 timestamp.`, {
        field: name,
      });
    }
    return new Date(raw).getTime();
  };

  const clientRef = (principal) =>
    principal.client ?? { id: principal.id, source: principal.source };

  const parseId = (raw, field = 'id') => {
    const value = Number(raw);
    if (!/^[1-9]\d*$/.test(raw) || !Number.isSafeInteger(value)) {
      throw new ApiError(400, 'invalid_request', `${field} must be a positive integer.`, {
        field,
      });
    }
    return value;
  };

  const withIdempotency = (principal, operation, key, body, action) => {
    if (
      typeof key !== 'string' ||
      key.length < 1 ||
      key.length > 128 ||
      !/^[\x20-\x7E]+$/.test(key)
    ) {
      throw new ApiError(
        400,
        'invalid_request',
        'Idempotency-Key must be 1-128 printable ASCII characters.',
        { field: 'Idempotency-Key' },
      );
    }

    const storageKey = `${principal.id}:${operation}:${key}`;
    const hash = sha256(stableStringify(body));
    const existing = idempotency.get(storageKey);
    if (existing) {
      if (Date.now() - existing.createdAt <= idempotencyTtlMs) {
        if (existing.hash !== hash) {
          throw new ApiError(
            409,
            'idempotency_key_reused',
            'This Idempotency-Key was already used with a different payload.',
          );
        }
        return { status: 200, body: { ...existing.response, replayed: true } };
      }
      idempotency.delete(storageKey);
    }

    const result = action();
    idempotency.set(storageKey, { hash, response: result.body, createdAt: Date.now() });
    return result;
  };

  const latestJobForRecord = (recordId) =>
    jobs
      .filter((job) => job.record_id === recordId)
      .sort((a, b) => b.id - a.id)
      .at(0) ?? null;

  const ingest = (principal, body, request) => {
    if (!principal.source) {
      throw new ApiError(403, 'forbidden', 'This client has no knowledge source.', {
        required_kind: 'agent',
      });
    }
    if (!validateIngest(body)) {
      const error = validateIngest.errors[0];
      const field = error.instancePath
        ? error.instancePath.slice(1).replaceAll('/', '.')
        : (error.params?.additionalProperty ?? '');
      throw new ApiError(
        422,
        'validation_failed',
        `${field ? `${field} ` : ''}${error.message}`,
        field ? { field } : undefined,
      );
    }

    const idempotencyKey = request.headers['idempotency-key'] ?? body.bundle_id;

    return withIdempotency(principal, 'ingest', idempotencyKey, body, () => {
      const timestamp = now();
      const key = `${principal.id}:${principal.source}:${body.external_id}`;
      const existing = recordsByKey.get(key);
      const rawText = rawTextFromMessages(body.messages);
      const contentHash = sha256(
        stableStringify({
          title: body.title,
          messages: body.messages,
          metadata: body.metadata,
          final: body.final,
        }),
      );
      touchProject(body.project_key, body.project_path);

      let record;
      let job = null;

      if (existing) {
        const changed = existing.content_hash !== contentHash;
        if (!changed) {
          const existingJob = latestJobForRecord(existing.id);
          const active = existingJob && ['queued', 'running'].includes(existingJob.status);
          return {
            status: 200,
            body: {
              record_id: existing.id,
              job_id: active ? existingJob.id : null,
              status: active ? 'queued' : 'stored',
              replayed: true,
            },
          };
        }

        existing.title = body.title;
        existing.raw_text = rawText;
        existing.content_hash = contentHash;
        existing.revision += 1;
        existing.is_final = body.final;
        existing.finalized_at = body.final ? timestamp : existing.finalized_at;
        existing.updated_at = body.updated_at;
        existing.indexed_at = timestamp;
        existing.metadata = body.metadata;
        existing.card_status = body.final ? 'queued' : 'heuristic';
        record = existing;
      } else {
        record = buildRecord({
          id: nextId('record'),
          client: clientRef(principal),
          contentHash,
          body: {
            external_id: body.external_id,
            project_key: body.project_key,
            kind: 'session',
            title: body.title,
            summary: '',
            raw_text: rawText,
            is_final: body.final,
            started_at: body.started_at,
            updated_at: body.updated_at,
            card_status: body.final ? 'queued' : 'heuristic',
            metadata: body.metadata,
          },
          nowIso: timestamp,
        });
        records.push(record);
        recordsById.set(record.id, record);
        recordsByKey.set(key, record);
      }

      if (body.final) {
        job = enqueueJob({
          client: clientRef(principal),
          kind: 'ingest',
          externalId: body.external_id,
          recordId: record.id,
        });
      }

      return {
        status: 202,
        body: {
          record_id: record.id,
          job_id: job?.id ?? null,
          status: job ? 'queued' : 'stored',
          replayed: false,
        },
      };
    });
  };

  const createMemory = (principal, body, request) => {
    if (!principal.source) {
      throw new ApiError(403, 'forbidden', 'This client kind cannot write memories.', {
        required_kind: 'agent',
      });
    }
    validateMemory(body);

    const contentHash = sha256(stableStringify(body));
    const externalId = body.external_id ?? `auto_${contentHash.slice(0, 32)}`;
    const projectKey = body.project_key ?? 'jarvis';
    const occurredAt = parseDate(body.occurred_at, 'occurred_at');
    touchProject(projectKey, '');

    const idempotencyKey = request.headers['idempotency-key'] ?? `content:${contentHash}`;
    return withIdempotency(principal, 'memory', idempotencyKey, body, () => {
      const key = `${principal.id}:${principal.source}:${externalId}`;
      const existing = recordsByKey.get(key);
      if (existing) {
        if (existing.content_hash !== contentHash) {
          throw new ApiError(409, 'conflict', 'external_id already exists with different content.', {
            field: 'external_id',
          });
        }
        const existingJob = latestJobForRecord(existing.id);
        const active = existingJob && ['queued', 'running'].includes(existingJob.status);
        return {
          status: 200,
          body: {
            record_id: existing.id,
            job_id: active ? existingJob.id : null,
            status: existing.card_status === 'ok' ? 'indexed' : 'queued',
            replayed: true,
          },
        };
      }

      const hasCard = Boolean(body.title && body.summary);
      const timestamp = now();
      const record = buildRecord({
        id: nextId('record'),
        client: clientRef(principal),
        contentHash,
        body: {
          external_id: externalId,
          project_key: projectKey,
          kind: 'memory',
          title: body.title ?? body.raw_text.split(/\r?\n/)[0].slice(0, 200),
          summary: body.summary ?? '',
          questions: body.questions,
          keywords: body.keywords,
          atoms: body.atoms,
          raw_text: body.raw_text,
          is_final: true,
          started_at: occurredAt ?? timestamp,
          updated_at: timestamp,
          card_status: hasCard ? 'ok' : 'queued',
          metadata: body.metadata ?? {},
        },
        nowIso: timestamp,
      });
      records.push(record);
      recordsById.set(record.id, record);
      recordsByKey.set(key, record);

      let job = null;
      if (!hasCard) {
        job = enqueueJob({
          client: clientRef(principal),
          kind: 'memory',
          externalId,
          recordId: record.id,
        });
      }

      return {
        status: 201,
        body: {
          record_id: record.id,
          job_id: job?.id ?? null,
          status: job ? 'queued' : 'indexed',
          replayed: false,
        },
      };
    });
  };

  const search = (principal, url) => {
    void principal;
    const query = (url.searchParams.get('q') ?? '').trim();
    if (!query) {
      throw new ApiError(400, 'invalid_request', 'q is required.', { field: 'q' });
    }
    const limit = parseLimit(url, { defaultValue: 8, min: 1, max: 50 });
    const projectKey = url.searchParams.get('project_key');
    const rawSource = url.searchParams.get('source');
    const source = rawSource === null ? null : rawSource.trim();
    if (source !== null && (source.length < 1 || source.length > 100)) {
      throw new ApiError(400, 'invalid_request', 'source must be 1..100 characters.', {
        field: 'source',
      });
    }
    const since = parseDateFilter(url, 'since');
    const until = parseDateFilter(url, 'until');

    const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
    const matches = [];
    for (const record of records) {
      if (projectKey && record.project_key !== projectKey) continue;
      if (source && record.source !== source) continue;
      const updated = Date.parse(record.updated_at);
      if (since !== null && updated < since) continue;
      if (until !== null && updated > until) continue;

      const fields = [
        [record.title, 6],
        [record.summary, 4],
        [record.questions.join(' '), 4],
        [record.keywords.join(' '), 5],
        [record.atoms.join(' '), 3],
        [record.raw_text, 1],
      ].map(([value, weight]) => [value.toLowerCase(), weight]);

      let score = 0;
      for (const term of terms) {
        for (const [value, weight] of fields) {
          if (value.includes(term)) score += weight;
        }
      }
      if (score > 0) matches.push({ record, score });
    }

    matches.sort(
      (a, b) =>
        b.score - a.score || Date.parse(b.record.updated_at) - Date.parse(a.record.updated_at),
    );

    return {
      status: 200,
      body: {
        items: matches.slice(0, limit).map(({ record, score }) => ({
          ...cardFields(record),
          score: Math.round(score * 1000) / 1000,
        })),
        total: matches.length,
        query,
        limit,
      },
    };
  };

  const listProjects = (url) => {
    const limit = parseLimit(url, { defaultValue: 100, min: 1, max: 500 });
    const items = [...projects.values()]
      .map((project) => {
        const owned = records.filter((record) => record.project_key === project.key);
        return {
          key: project.key,
          name: project.name,
          path: project.path,
          record_count: owned.length,
          last_activity_at: owned.reduce(
            (latest, record) =>
              !latest || Date.parse(record.updated_at) > Date.parse(latest) ? record.updated_at : latest,
            null,
          ),
          created_at: project.created_at,
        };
      })
      .sort((a, b) => {
        const aTime = a.last_activity_at ? Date.parse(a.last_activity_at) : 0;
        const bTime = b.last_activity_at ? Date.parse(b.last_activity_at) : 0;
        return bTime - aTime || a.key.localeCompare(b.key);
      })
      .slice(0, limit);

    return { status: 200, body: { items } };
  };

  const graph = (url) => {
    const limit = parseLimit(url, { defaultValue: 100, min: 1, max: 500 });
    const projectKey = url.searchParams.get('project_key');
    const rawSource = url.searchParams.get('source');
    const source = rawSource === null ? null : rawSource.trim();
    if (source !== null && (source.length < 1 || source.length > 100)) {
      throw new ApiError(400, 'invalid_request', 'source must be 1..100 characters.', {
        field: 'source',
      });
    }
    const rawMinWeight = url.searchParams.get('min_weight');
    const minWeight = rawMinWeight === null ? 0 : Number(rawMinWeight);
    if (!Number.isFinite(minWeight) || minWeight < 0 || minWeight > 1) {
      throw new ApiError(400, 'invalid_request', 'min_weight must be a number from 0 to 1.', {
        field: 'min_weight',
      });
    }

    const eligible = records
      .filter((record) => !projectKey || record.project_key === projectKey)
      .filter((record) => !source || record.source === source)
      .sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at));
    const included = eligible.slice(0, limit);
    const nodes = included.map((record) => ({
      id: record.id,
      title: record.title,
      project_key: record.project_key,
      source: record.source,
      kind: record.kind,
      updated_at: record.updated_at,
    }));

    const edges = [];
    for (let i = 0; i < included.length; i += 1) {
      for (let j = i + 1; j < included.length; j += 1) {
        const left = included[i];
        const right = included[j];
        const sharedKeywords = left.keywords.filter((keyword) =>
          right.keywords.some((other) => other.toLowerCase() === keyword.toLowerCase()),
        );
        let weight = 0;
        let reason = null;
        if (sharedKeywords.length > 0) {
          weight = Math.min(0.9, 0.5 + sharedKeywords.length * 0.1);
          reason = 'keywords';
        } else if (left.project_key === right.project_key) {
          weight = 0.4;
          reason = 'project';
        }
        if (reason && weight >= minWeight) {
          edges.push({ from_id: left.id, to_id: right.id, weight, reason });
        }
      }
    }

    return {
      status: 200,
      body: { nodes, edges, truncated: eligible.length > included.length },
    };
  };

  const stats = () => {
    const countBy = (values) =>
      values.reduce((counts, value) => {
        counts[value] = (counts[value] ?? 0) + 1;
        return counts;
      }, {});
    const sourceCounts = countBy(records.map((record) => record.source));
    const cardCounts = countBy(records.map((record) => record.card_status));
    const jobCounts = countBy(jobs.map((job) => job.status));
    const lastIngest = records.reduce(
      (latest, record) => (!latest || record.updated_at > latest ? record.updated_at : latest),
      null,
    );

    return {
      status: 200,
      body: {
        records: {
          total: records.length,
          final: records.filter((record) => record.is_final).length,
          draft: records.filter((record) => !record.is_final).length,
          by_source: sourceCounts,
          by_card_status: {
            heuristic: cardCounts.heuristic ?? 0,
            queued: cardCounts.queued ?? 0,
            ok: cardCounts.ok ?? 0,
            failed: cardCounts.failed ?? 0,
          },
        },
        projects: projects.size,
        clients: clients.filter((client) => !client.revoked_at).length,
        jobs: {
          queued: jobCounts.queued ?? 0,
          running: jobCounts.running ?? 0,
          done: jobCounts.done ?? 0,
          failed: jobCounts.failed ?? 0,
        },
        last_ingest_at: lastIngest,
        database: {
          status: 'ok',
          schema_version: 4,
          size_bytes: Buffer.byteLength(JSON.stringify(records)),
        },
      },
    };
  };

  const listJobs = (url) => {
    const limit = parseLimit(url, { defaultValue: 50, min: 1, max: 200 });
    const status = url.searchParams.get('status');
    if (status !== null && !JOB_STATUSES.has(status)) {
      throw new ApiError(400, 'invalid_request', 'status must be queued, running, done or failed.', {
        field: 'status',
      });
    }

    let cursorId = null;
    const cursor = url.searchParams.get('cursor');
    if (cursor !== null) {
      try {
        cursorId = Number(Buffer.from(cursor, 'base64url').toString('utf8'));
      } catch {
        cursorId = Number.NaN;
      }
      if (!Number.isInteger(cursorId) || cursorId < 0) {
        throw new ApiError(400, 'invalid_request', 'cursor is invalid.', { field: 'cursor' });
      }
    }

    const filtered = jobs
      .filter((job) => (status === null ? true : job.status === status))
      .filter((job) => (cursorId === null ? true : job.id < cursorId))
      .sort((a, b) => b.id - a.id);
    const page = filtered.slice(0, limit);
    const hasMore = filtered.length > limit;

    return {
      status: 200,
      body: {
        items: page,
        next_cursor: hasMore ? Buffer.from(String(page.at(-1).id)).toString('base64url') : null,
      },
    };
  };

  const listClients = () => ({
    status: 200,
    body: { items: clients.map(clientFields) },
  });

  const createClient = (body) => {
    const spec = normalizeClientRequest(body);
    if (clients.some((client) => !client.revoked_at && client.name === spec.name)) {
      throw new ApiError(409, 'conflict', 'An active client with this name already exists.', {
        field: 'name',
      });
    }
    if (
      spec.source !== null &&
      clients.some((client) => !client.revoked_at && client.source === spec.source)
    ) {
      throw new ApiError(409, 'conflict', 'An active client with this source already exists.', {
        field: 'source',
      });
    }

    const timestamp = now();
    const token = `sb_${spec.kind}_${randomBytes(32).toString('base64url')}`;
    const client = {
      id: nextId('client'),
      name: spec.name,
      kind: spec.kind,
      scopes: spec.scopes,
      source: spec.source,
      created_at: timestamp,
      last_seen_at: null,
      revoked_at: null,
    };
    clients.push(client);
    tokens.set(tokenHash(token), client);

    return {
      status: 201,
      body: { client: clientFields(client), token },
    };
  };

  const revokeClient = (id) => {
    const client = clients.find((item) => item.id === id);
    if (!client) throw new ApiError(404, 'not_found', 'Client not found.');
    if (!client.revoked_at) client.revoked_at = now();
    return { status: 204, body: null };
  };

  const routes = [
    {
      method: 'GET',
      pattern: '/health',
      public: true,
      scopes: [],
      handler: () => ({
        status: 200,
        body: {
          status: 'ok',
          version: 'mock-1.0.0',
          schema_version: 4,
          time: now(),
          database: { status: 'ok', fts5: true },
        },
      }),
    },
    {
      method: 'POST',
      pattern: '/api/v1/ingest',
      scopes: ['ingest'],
      readBody: true,
      handler: (context) => ingest(context.principal, context.body, context.request),
    },
    {
      method: 'POST',
      pattern: '/api/v1/memories',
      scopes: ['ingest'],
      readBody: true,
      handler: (context) => createMemory(context.principal, context.body, context.request),
    },
    {
      method: 'GET',
      pattern: '/api/v1/search',
      scopes: ['ui', 'mcp:read'],
      handler: (context) => search(context.principal, context.url),
    },
    {
      method: 'GET',
      pattern: /^\/api\/v1\/records\/([^/]+)$/,
      scopes: ['ui', 'mcp:read'],
      handler: (context) => {
        const record = recordsById.get(parseId(context.params[0]));
        if (!record) throw new ApiError(404, 'not_found', 'Record not found.');
        return { status: 200, body: recordFields(record) };
      },
    },
    {
      method: 'GET',
      pattern: '/api/v1/projects',
      scopes: ['ui', 'mcp:read'],
      handler: (context) => listProjects(context.url),
    },
    {
      method: 'GET',
      pattern: '/api/v1/graph',
      scopes: ['ui'],
      handler: (context) => graph(context.url),
    },
    {
      method: 'GET',
      pattern: '/api/v1/stats',
      scopes: ['ui', 'mcp:read'],
      handler: () => stats(),
    },
    {
      method: 'GET',
      pattern: '/api/v1/clients',
      scopes: ['admin'],
      handler: () => listClients(),
    },
    {
      method: 'POST',
      pattern: '/api/v1/clients',
      scopes: ['admin'],
      readBody: true,
      handler: (context) => createClient(context.body),
    },
    {
      method: 'DELETE',
      pattern: /^\/api\/v1\/clients\/([^/]+)$/,
      scopes: ['admin'],
      handler: (context) => revokeClient(parseId(context.params[0])),
    },
    {
      method: 'GET',
      pattern: '/api/v1/jobs',
      scopes: ['admin'],
      handler: (context) => listJobs(context.url),
    },
  ];

  const matchPath = (pattern, pathname) => {
    if (typeof pattern === 'string') return pattern === pathname ? [] : null;
    const match = pattern.exec(pathname);
    return match ? match.slice(1) : null;
  };

  const send = (response, status, body, requestId, extraHeaders = {}) => {
    const headers = {
      'x-request-id': requestId,
      ...extraHeaders,
    };
    if (status === 204) {
      response.writeHead(status, headers);
      response.end();
      return;
    }
    const payload = JSON.stringify(body);
    response.writeHead(status, {
      ...headers,
      'content-type': 'application/json; charset=utf-8',
      'content-length': Buffer.byteLength(payload),
    });
    response.end(payload);
  };

  const sendError = (response, status, code, message, details, requestId, extraHeaders = {}) => {
    const error = { code, message };
    if (details !== undefined) error.details = details;
    error.request_id = requestId;
    send(response, status, { error }, requestId, extraHeaders);
  };

  const server = http.createServer(async (request, response) => {
    const requestId = `req_${randomBytes(6).toString('hex')}`;
    const url = new URL(request.url ?? '/', `http://${request.headers.host ?? 'localhost'}`);
    const pathMatches = routes
      .map((route) => ({ route, params: matchPath(route.pattern, url.pathname) }))
      .filter((entry) => entry.params !== null);

    if (pathMatches.length === 0) {
      sendError(response, 404, 'not_found', 'Route not found.', undefined, requestId);
      return;
    }

    const entry = pathMatches.find((item) => item.route.method === request.method);
    if (!entry) {
      const allow = pathMatches.map((item) => item.route.method).join(', ');
      sendError(
        response,
        405,
        'method_not_allowed',
        `Method ${request.method} is not allowed for ${url.pathname}.`,
        { allow: allow.split(', ') },
        requestId,
        { allow },
      );
      return;
    }

    try {
      let principal = null;
      if (!entry.route.public) {
        principal = authPrincipal(request);
        if (!principal) {
          throw new ApiError(401, 'unauthorized', 'A valid bearer token is required.');
        }
        requireScope(principal, entry.route.scopes);
      }

      const body = entry.route.readBody ? await readJson(request) : null;
      const result = entry.route.handler({
        principal,
        request,
        url,
        body,
        params: entry.params,
      });
      send(response, result.status, result.body, requestId, result.headers);
    } catch (error) {
      if (error instanceof ApiError) {
        sendError(response, error.status, error.code, error.message, error.details, requestId);
      } else {
        log('mock-api error:', error);
        sendError(response, 500, 'internal_error', 'Internal server error.', undefined, requestId);
      }
    }
  });

  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(port, host, resolve);
  });

  const address = server.address();
  const actualPort = typeof address === 'object' && address ? address.port : port;
  const shownHost = host.includes(':') ? `[${host}]` : host;
  const url = `http://${shownHost}${DEFAULT_PORTS.has(String(actualPort)) ? '' : `:${actualPort}`}`;

  return {
    server,
    url,
    adminToken,
    close: () => new Promise((resolve) => server.close(resolve)),
  };
}

const isEntrypoint =
  process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url;

if (isEntrypoint) {
  const port = Number(process.env.PORT ?? 8080);
  const host = process.env.HOST ?? '127.0.0.1';
  const adminToken = process.env.BRAIN_ADMIN_TOKEN ?? DEFAULT_ADMIN_TOKEN;

  startMockApi({ port, host, adminToken }).then((api) => {
    console.log(`Second Brain mock API listening on ${api.url}`);
    console.log(`Admin token: ${api.adminToken}`);
    const shutdown = async () => {
      await api.close();
      process.exit(0);
    };
    process.on('SIGINT', shutdown);
    process.on('SIGTERM', shutdown);
  });
}
