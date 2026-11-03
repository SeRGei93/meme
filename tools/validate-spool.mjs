#!/usr/bin/env node
import { readFile } from 'node:fs/promises';
import {
  createSpoolValidators,
  formatAjvErrors,
} from './lib/spool-schema.mjs';

const USAGE = `Usage: node tools/validate-spool.mjs [--stdin | <bundle.json> ...]

Validates spool bundles against docs/spool.schema.json.
Exit codes: 0 all valid, 1 invalid input, 2 usage error.`;

const args = process.argv.slice(2);
if (args.includes('--help') || args.includes('-h')) {
  console.log(USAGE);
  process.exit(0);
}

const unknownOptions = args.filter((arg) => arg.startsWith('-') && arg !== '--stdin');
if (unknownOptions.length > 0) {
  console.error(`Unknown option: ${unknownOptions[0]}\n\n${USAGE}`);
  process.exit(2);
}

const useStdin = args.includes('--stdin');
const files = args.filter((arg) => arg !== '--stdin');

if (useStdin && files.length > 0) {
  console.error(`--stdin cannot be combined with file arguments.\n\n${USAGE}`);
  process.exit(2);
}

const readStdin = async () => {
  const chunks = [];
  for await (const chunk of process.stdin) chunks.push(chunk);
  return Buffer.concat(chunks).toString('utf8');
};

let inputs;
if (useStdin || (files.length === 0 && !process.stdin.isTTY)) {
  inputs = [{ name: '<stdin>', load: readStdin }];
} else if (files.length > 0) {
  inputs = files.map((file) => ({ name: file, load: () => readFile(file, 'utf8') }));
} else {
  console.error(USAGE);
  process.exit(2);
}

const { validateBundle } = await createSpoolValidators();
let failed = false;

for (const input of inputs) {
  let bundle;
  try {
    bundle = JSON.parse(await input.load());
  } catch (error) {
    failed = true;
    console.error(`invalid: ${input.name}`);
    console.error(`  / ${error.message}`);
    continue;
  }

  if (validateBundle(bundle)) {
    console.log(`valid: ${input.name}`);
    continue;
  }

  failed = true;
  console.error(`invalid: ${input.name}`);
  for (const message of formatAjvErrors(validateBundle.errors)) {
    console.error(`  ${message}`);
  }
}

process.exit(failed ? 1 : 0);
