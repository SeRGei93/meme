import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import YAML from 'yaml';

const OPENAPI_PATH = fileURLToPath(new URL('../../docs/openapi.yaml', import.meta.url));

const EXPECTED_OPERATIONS = {
  '/health': { get: [] },
  '/api/v1/ingest': { post: ['ingest'] },
  '/api/v1/memories': { post: ['ingest'] },
  '/api/v1/search': { get: ['ui', 'mcp:read'] },
  '/api/v1/records/{id}': { get: ['ui', 'mcp:read'] },
  '/api/v1/projects': { get: ['ui', 'mcp:read'] },
  '/api/v1/graph': { get: ['ui'] },
  '/api/v1/stats': { get: ['ui', 'mcp:read'] },
  '/api/v1/clients': { get: ['admin'], post: ['admin'] },
  '/api/v1/clients/{id}': { delete: ['admin'] },
  '/api/v1/jobs': { get: ['admin'] },
};

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

async function loadSpec() {
  return YAML.parse(await readFile(OPENAPI_PATH, 'utf8'));
}

test('OpenAPI 3.1 document covers every v1 path and method', async () => {
  const spec = await loadSpec();
  assert.equal(spec.openapi, '3.1.0');
  assert.equal(typeof spec.info?.title, 'string');

  for (const [path, methods] of Object.entries(EXPECTED_OPERATIONS)) {
    assert.ok(spec.paths?.[path], `missing path ${path}`);
    for (const method of Object.keys(methods)) {
      assert.ok(spec.paths[path][method], `missing ${method.toUpperCase()} ${path}`);
    }
  }
});

test('every operation declares its scope requirements', async () => {
  const spec = await loadSpec();
  const validScopes = new Set(['ingest', 'mcp:read', 'mcp:write', 'ui', 'admin']);

  for (const [path, methods] of Object.entries(EXPECTED_OPERATIONS)) {
    for (const [method, expectedScopes] of Object.entries(methods)) {
      const operation = spec.paths[path][method];
      assert.deepEqual(
        operation['x-required-scopes'],
        expectedScopes,
        `${method.toUpperCase()} ${path} has wrong x-required-scopes`,
      );
      for (const scope of operation['x-required-scopes']) {
        assert.ok(validScopes.has(scope), `${scope} is not a v1 scope`);
      }

      if (expectedScopes.length === 0) {
        assert.deepEqual(operation.security, [], `${method.toUpperCase()} ${path} must be public`);
      } else {
        assert.deepEqual(
          operation.security,
          [{ bearerAuth: [] }],
          `${method.toUpperCase()} ${path} must require bearer auth`,
        );
      }
    }
  }
});

test('error envelope and every v1 error code are specified', async () => {
  const spec = await loadSpec();
  const schema = spec.components?.schemas?.Error;

  assert.equal(schema?.type, 'object');
  assert.deepEqual(schema.required?.sort(), ['code', 'message']);
  assert.deepEqual(
    [...schema.properties.code.enum].sort(),
    [...ERROR_CODES].sort(),
  );
  assert.ok(spec.components.schemas.ErrorEnvelope, 'missing ErrorEnvelope schema');
});

test('all documented responses reference declared schemas', async () => {
  const spec = await loadSpec();
  const refs = [];
  const walk = (node) => {
    if (!node || typeof node !== 'object') return;
    if (typeof node.$ref === 'string' && node.$ref.startsWith('#/components/')) {
      refs.push(node.$ref);
    }
    for (const value of Object.values(node)) walk(value);
  };
  walk(spec);

  for (const ref of refs) {
    const parts = ref.replace('#/', '').split('/');
    let target = spec;
    for (const part of parts) target = target?.[part];
    assert.ok(target, `unresolved local $ref ${ref}`);
  }
});
