import type {
  AssistantContentLike,
  NormalizeInput,
  NormalizeLimits,
  SessionMessageLike,
  SpoolBundle,
  SpoolMessage,
  SpoolMetadata,
  SpoolToolCall,
} from './types.ts';

const DEFAULT_LIMITS: NormalizeLimits = {
  maxMessageChars: 200_000,
  maxMessages: 10_000,
  maxToolCallsPerMessage: 500,
  maxToolFieldChars: 2_000,
};

const MAX_TITLE_CHARS = 500;
const MAX_TOOL_NAME_CHARS = 200;
const MAX_EXTERNAL_ID_CHARS = 200;
const MAX_PROJECT_KEY_CHARS = 500;
const MAX_PROJECT_PATH_CHARS = 1_000;
const MAX_AGENT_CHARS = 100;
const MAX_MODEL_CHARS = 200;
const MAX_BRANCH_CHARS = 200;
const MAX_VERSION_CHARS = 100;

/**
 * Converts an OpenCode session and its context messages into a spool bundle.
 *
 * Reasoning blocks, tool outputs and non user/assistant messages are dropped
 * on purpose: the spool keeps what a human would search for, not the full
 * transcript. Returns undefined when the session has no usable messages, so
 * the caller can skip the write and leave the cursor untouched.
 */
export function normalizeSession(input: NormalizeInput): SpoolBundle | undefined {
  const limits = { ...DEFAULT_LIMITS, ...input.limits };
  const normalized: SpoolMessage[] = [];

  for (const message of input.messages) {
    if (message.type !== 'user' && message.type !== 'assistant') continue;
    const mapped = mapMessage(message, limits, input.session.time.created);
    if (mapped) normalized.push(mapped);
  }

  const ordered = normalized
    .map((message, index) => ({ message, index, at: Date.parse(message.at), seq: index }))
    .sort((a, b) => a.at - b.at || a.seq - b.seq)
    .map((entry) => entry.message);

  const messages = ordered.slice(-limits.maxMessages);
  if (messages.length === 0) return undefined;

  const metadata: SpoolMetadata = {};
  if (input.session.agent) metadata.agent = truncate(input.session.agent, MAX_AGENT_CHARS);
  if (input.session.model) {
    metadata.model = truncate(`${input.session.model.providerID}/${input.session.model.id}`, MAX_MODEL_CHARS);
  }
  if (input.branch) metadata.branch = truncate(input.branch, MAX_BRANCH_CHARS);
  if (input.opencodeVersion) metadata.opencode_version = truncate(input.opencodeVersion, MAX_VERSION_CHARS);

  const started = input.session.time.created;
  const messageEnd = Math.max(...messages.map((message) => Date.parse(message.at)));
  const updated = Math.max(started, messageEnd);

  return {
    schema_version: 1,
    bundle_id: input.bundleId,
    source: 'opencode',
    external_id: truncate(input.session.id, MAX_EXTERNAL_ID_CHARS),
    project_key: truncate(input.projectKey, MAX_PROJECT_KEY_CHARS),
    project_path: truncate(input.projectPath, MAX_PROJECT_PATH_CHARS),
    title: deriveTitle(input, messages),
    final: input.final,
    started_at: new Date(started).toISOString(),
    updated_at: new Date(updated).toISOString(),
    messages,
    metadata,
  };
}

function mapMessage(message: SessionMessageLike, limits: NormalizeLimits, fallbackAt: number): SpoolMessage | undefined {
  const rawAt = message.time?.completed ?? message.time?.created;
  const at = rawAt ?? fallbackAt;
  const id = truncate(message.id, MAX_EXTERNAL_ID_CHARS);
  if (message.type === 'user') {
    const text = truncate(message.text ?? '', limits.maxMessageChars);
    if (text.trim() === '') return undefined;
    return { id, role: 'user', text, at: new Date(at).toISOString() };
  }

  const texts: string[] = [];
  const toolCalls: SpoolToolCall[] = [];
  for (const part of message.content ?? []) {
    if (part.type === 'text') {
      texts.push(part.text);
    } else if (part.type === 'tool') {
      if (toolCalls.length >= limits.maxToolCallsPerMessage) continue;
      toolCalls.push(mapToolCall(part, limits));
    }
    // reasoning parts are intentionally skipped
  }

  const text = truncate(texts.join('\n\n'), limits.maxMessageChars);
  if (text.trim() === '' && toolCalls.length === 0) return undefined;

  const result: SpoolMessage = {
    id,
    role: 'assistant',
    text,
    at: new Date(at).toISOString(),
  };
  if (toolCalls.length > 0) result.tool_calls = toolCalls;
  return result;
}

function mapToolCall(part: Extract<AssistantContentLike, { type: 'tool' }>, limits: NormalizeLimits): SpoolToolCall {
  const input = part.state?.input ?? {};
  const call: SpoolToolCall = {
    name: truncate(part.name, MAX_TOOL_NAME_CHARS) || 'unknown',
  };
  const command = firstString(input, ['command']);
  const path = firstString(input, ['filePath', 'path']);
  if (command !== undefined) call.command = truncate(command, limits.maxToolFieldChars);
  if (path !== undefined) call.path = truncate(path, limits.maxToolFieldChars);
  return call;
}

function firstString(input: Record<string, unknown>, keys: readonly string[]): string | undefined {
  for (const key of keys) {
    const value = input[key];
    if (typeof value === 'string' && value !== '') return value;
  }
  return undefined;
}

function deriveTitle(input: NormalizeInput, messages: readonly SpoolMessage[]): string {
  const title = input.session.title?.trim();
  if (title) return truncate(title, MAX_TITLE_CHARS);
  const firstUser = messages.find((message) => message.role === 'user' && message.text.trim() !== '');
  if (firstUser) {
    const line = firstUser.text.split('\n')[0]?.trim() ?? '';
    if (line) return truncate(line, MAX_TITLE_CHARS);
  }
  return truncate(`Session ${input.session.id}`, MAX_TITLE_CHARS);
}

function truncate(value: string, max: number): string {
  return value.length <= max ? value : value.slice(0, max);
}
