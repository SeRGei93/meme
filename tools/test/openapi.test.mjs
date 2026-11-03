import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import YAML from 'yaml';
import Ajv2020 from 'ajv/dist/2020.js';

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

function rewriteComponentRefs(node) {
  if (Array.isArray(node)) {
    node.forEach(rewriteComponentRefs);
    return;
  }
  if (!node || typeof node !== 'object') return;
  if (typeof node.$ref === 'string' && node.$ref.startsWith('#/components/schemas/')) {
    node.$ref = node.$ref.replace('#/components/schemas/', '#/$defs/');
  }
  for (const value of Object.values(node)) rewriteComponentRefs(value);
}

function compileComponent(spec, name) {
  const schemas = structuredClone(spec.components.schemas);
  rewriteComponentRefs(schemas);
  const ajv = new Ajv2020({ allErrors: true, strict: false, validateFormats: false });
  return ajv.compile({
    $schema: 'https://json-schema.org/draft/2020-12/schema',
    $defs: schemas,
    $ref: `#/$defs/${name}`,
  });
}

test('every response example validates against its OpenAPI schema', async () => {
  const spec = await loadSpec();
  let checked = 0;

  for (const [path, item] of Object.entries(spec.paths)) {
    for (const [method, operation] of Object.entries(item)) {
      for (const response of Object.values(operation.responses ?? {})) {
        if (!response || response.$ref) continue;
        const media = response.content?.['application/json'];
        if (!media?.schema?.$ref) continue;

        const examples = media.examples
          ? Object.values(media.examples).map((example) => example.value)
          : media.example !== undefined
            ? [media.example]
            : [];
        if (examples.length === 0) continue;

        const name = media.schema.$ref.split('/').at(-1);
        const validate = compileComponent(spec, name);
        for (const example of examples) {
          assert.equal(
            validate(example),
            true,
            `${method.toUpperCase()} ${path} example violates ${name}: ${JSON.stringify(validate.errors)}`,
          );
          checked += 1;
        }
      }
    }
  }

  assert.ok(checked >= 5, `expected at least 5 response examples, checked ${checked}`);
});
