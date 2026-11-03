import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';

export const DEFAULT_SPOOL_SCHEMA_PATH = fileURLToPath(
  new URL('../../docs/spool.schema.json', import.meta.url),
);

export async function loadSpoolSchema(path = DEFAULT_SPOOL_SCHEMA_PATH) {
  return JSON.parse(await readFile(path, 'utf8'));
}

/**
 * Compile the spool contract once and reuse it for bundle and ingest validation.
 * Both validators use the same $defs, so the wire contract and the local schema
 * cannot drift.
 */
export function compileSpoolSchema(schema) {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);

  return {
    validateBundle: ajv.compile(schema),
    validateIngest: ajv.compile({
      $schema: schema.$schema,
      $defs: schema.$defs,
      $ref: '#/$defs/IngestRequest',
    }),
  };
}

export async function createSpoolValidators(path = DEFAULT_SPOOL_SCHEMA_PATH) {
  return compileSpoolSchema(await loadSpoolSchema(path));
}

export function formatAjvErrors(errors = []) {
  return errors.map((error) => {
    const path = error.instancePath || '/';
    return `${path} ${error.message}`;
  });
}
