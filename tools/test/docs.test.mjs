import assert from 'node:assert/strict';
import { readFile, stat } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import YAML from 'yaml';

const ROOT = fileURLToPath(new URL('../..', import.meta.url));
const DOCS = join(ROOT, 'docs');
const CONTRACTS = join(DOCS, 'CONTRACTS.md');
const MCP = join(DOCS, 'mcp-tools.md');
const OPENAPI = join(DOCS, 'openapi.yaml');
const SPOOL = join(DOCS, 'spool.schema.json');

const ERROR_CODES = [
  'invalid_request',
  'validation_failed',
  'unauthorized',
  'forbidden',
  'not_found',
  'method_not_allowed',
  'conflict',
  'idempotency_key_reused',
  'payload_too_large',
  'unsupported_media_type',
  'rate_limited',
  'internal_error',
  'service_unavailable',
];

test('CONTRACTS.md fixes errors, retries, idempotency and every scope', async () => {
  const doc = await readFile(CONTRACTS, 'utf8');

  for (const code of ERROR_CODES) {
    assert.ok(doc.includes(`\`${code}\``), `CONTRACTS.md misses error code ${code}`);
  }
  for (const scope of ['ingest', 'mcp:read', 'mcp:write', 'ui', 'admin']) {
    assert.ok(doc.includes(`\`${scope}\``), `CONTRACTS.md misses scope ${scope}`);
  }
  assert.match(doc, /source.*определяется токеном/is);
  assert.match(doc, /Idempotency-Key/);
  assert.match(doc, /24 часа/);
  assert.match(doc, /Retry-After/);
  assert.match(doc, /Экспоненциальный с full jitter/);
  assert.match(doc, /npm run mock/);
  assert.match(doc, /validate-spool/);
});

test('MCP contract covers all v1 tools, scopes, schemas and error format', async () => {
  const doc = await readFile(MCP, 'utf8');

  for (const tool of ['search', 'get_record', 'remember', 'list_projects', 'stats']) {
    assert.ok(doc.includes(`"name": "${tool}"`), `mcp-tools.md misses tool ${tool}`);
  }
  for (const scope of ['mcp:read', 'mcp:write']) {
    assert.ok(doc.includes(`\`${scope}\``), `mcp-tools.md misses scope ${scope}`);
  }
  assert.match(doc, /inputSchema/);
  assert.match(doc, /outputSchema/);
  assert.match(doc, /structuredContent/);
  assert.match(doc, /isError/);
  assert.match(doc, /tools\/call/);
  assert.match(doc, /remember[\s\S]*mcp:write/);
});

test('OpenAPI schemas do not drift from the spool schema', async () => {
  const spec = YAML.parse(await readFile(OPENAPI, 'utf8'));
  const spool = JSON.parse(await readFile(SPOOL, 'utf8'));

  const resolveRef = (document, ref) =>
    ref
      .slice(2)
      .split('/')
      .reduce(
        (node, part) => node?.[part.replaceAll('~1', '/').replaceAll('~0', '~')],
        document,
      );

  const shape = (document, node) => {
    if (Array.isArray(node)) return node.map((item) => shape(document, item));
    if (!node || typeof node !== 'object') return node;
    if (node.$ref) return shape(document, resolveRef(document, node.$ref));

    const out = {};
    for (const key of [
      'type',
      'format',
      'const',
      'minLength',
      'maxLength',
      'pattern',
      'minimum',
      'maximum',
      'minItems',
      'maxItems',
      'additionalProperties',
    ]) {
      if (key in node) out[key] = node[key];
    }
    if (node.enum) out.enum = [...node.enum];
    if (node.required) out.required = [...node.required].sort();
    if (node.items) out.items = shape(document, node.items);
    if (node.properties) {
      out.properties = Object.fromEntries(
        Object.entries(node.properties).map(([name, value]) => [name, shape(document, value)]),
      );
    }
    return out;
  };

  const pairs = [
    ['IngestRequest', 'IngestRequest'],
    ['Message', 'Message'],
    ['ToolCall', 'ToolCall'],
    ['BundleMetadata', 'Metadata'],
  ];

  for (const [openapiName, spoolName] of pairs) {
    const openapi = spec.components.schemas[openapiName];
    const schema = spool.$defs[spoolName];
    assert.ok(openapi, `OpenAPI misses ${openapiName}`);
    assert.ok(schema, `spool schema misses ${spoolName}`);
    assert.deepEqual(
      shape(spec, openapi),
      shape(spool, schema),
      `${openapiName} drifts from spool ${spoolName}`,
    );
  }

  assert.deepEqual(
    spec.components.schemas.MessageRole.enum,
    spool.$defs.MessageRole.enum,
  );
});

test('documentation links point at files in the repository', async () => {
  for (const path of [CONTRACTS, MCP]) {
    const doc = await readFile(path, 'utf8');
    const links = [...doc.matchAll(/\[[^\]]*\]\(([^)]+)\)/g)].map((match) => match[1]);
    for (const link of links) {
      if (link.startsWith('http') || link.startsWith('#')) continue;
      const target = link.split('#')[0];
      if (!target) continue;
      await assert.doesNotReject(
        stat(join(dirname(path), target)),
        `${path} links to missing ${target}`,
      );
    }
  }
});
