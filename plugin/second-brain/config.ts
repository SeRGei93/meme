import { readFile as readFileDefault } from 'node:fs/promises';
import { homedir } from 'node:os';
import { join } from 'node:path';

/**
 * Four minutes sits in the 3–5 minute window from the ticket: long enough not
 * to snapshot a session while the user is thinking, short enough that the app
 * uploads recent work before it goes stale.
 */
export const DEFAULT_IDLE_DEBOUNCE_MS = 4 * 60_000;

export interface PluginConfig {
  /** Directory that holds config.json and the default spool. */
  readonly configDir: string;
  /** Where normalized bundles are written. */
  readonly spoolDir: string;
  /** How long session.idle waits before capturing a live bundle. */
  readonly idleDebounceMs: number;
  /** Per-message text cap; matches docs/spool.schema.json. */
  readonly maxMessageChars: number;
  /** Per-bundle message cap; matches docs/spool.schema.json. */
  readonly maxMessages: number;
  /** Per-message tool call cap; matches docs/spool.schema.json. */
  readonly maxToolCallsPerMessage: number;
  /** Cap for tool name/command/path; matches docs/spool.schema.json. */
  readonly maxToolFieldChars: number;
}

export interface LoadConfigOptions {
  readonly home?: string;
  readonly env?: NodeJS.ProcessEnv;
  readonly readFile?: (path: string) => Promise<string>;
}

export function defaultConfig(configDir: string): PluginConfig {
  return {
    configDir,
    spoolDir: join(configDir, 'spool'),
    idleDebounceMs: DEFAULT_IDLE_DEBOUNCE_MS,
    maxMessageChars: 200_000,
    maxMessages: 10_000,
    maxToolCallsPerMessage: 500,
    maxToolFieldChars: 2_000,
  };
}

/**
 * Reads plugin settings. A missing, unreadable or malformed file is not an
 * error: the plugin must keep capturing with defaults, otherwise a single bad
 * edit would silently stop the whole pipeline.
 */
export async function loadConfig(options: LoadConfigOptions = {}): Promise<PluginConfig> {
  const home = options.home ?? homedir();
  const env = options.env ?? process.env;
  const readFile = options.readFile ?? ((path: string) => readFileDefault(path, 'utf8'));
  const configDir = join(env.XDG_CONFIG_HOME || join(home, '.config'), 'second-brain');
  const defaults = defaultConfig(configDir);

  let raw: string;
  try {
    raw = await readFile(join(configDir, 'config.json'));
  } catch {
    return defaults;
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return defaults;
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return defaults;
  const data = parsed as Record<string, unknown>;

  return {
    ...defaults,
    spoolDir: readSpoolDir(data, home, defaults),
    idleDebounceMs: readDebounce(data.idle_debounce_ms, defaults.idleDebounceMs),
    maxMessageChars: readBoundedInt(data.max_message_chars, defaults.maxMessageChars, 200_000),
    maxMessages: readBoundedInt(data.max_messages, defaults.maxMessages, 10_000),
    maxToolCallsPerMessage: readBoundedInt(data.max_tool_calls_per_message, defaults.maxToolCallsPerMessage, 500),
    maxToolFieldChars: readBoundedInt(data.max_tool_field_chars, defaults.maxToolFieldChars, 2_000),
  };
}

function readSpoolDir(data: Record<string, unknown>, home: string, defaults: PluginConfig): string {
  const value = data.spool_dir;
  if (typeof value !== 'string' || value.trim() === '') return defaults.spoolDir;
  if (value === '~') return home;
  if (value.startsWith('~/')) return join(home, value.slice(2));
  return value;
}

function readPositiveInt(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : fallback;
}

/** The ticket fixes the idle debounce at 3–5 minutes. */
function readDebounce(value: unknown, fallback: number): number {
  return Math.min(Math.max(readPositiveInt(value, fallback), 180_000), 300_000);
}

/**
 * Contract fields are capped by docs/spool.schema.json; a larger configured
 * value is clamped so the plugin can never emit a schema-invalid bundle.
 */
function readBoundedInt(value: unknown, fallback: number, max: number): number {
  const parsed = readPositiveInt(value, fallback);
  return Math.min(parsed, max);
}
