/** Shape of a spool bundle from docs/spool.schema.json. */
export interface SpoolToolCall {
  name: string;
  command?: string;
  path?: string;
}

export interface SpoolMessage {
  id: string;
  role: 'user' | 'assistant' | 'system' | 'tool';
  text: string;
  at: string;
  tool_calls?: SpoolToolCall[];
}

export interface SpoolMetadata {
  agent?: string;
  model?: string;
  branch?: string;
  opencode_version?: string;
}

export interface SpoolBundle {
  schema_version: 1;
  bundle_id: string;
  source: 'opencode';
  external_id: string;
  project_key: string;
  project_path: string;
  title: string;
  final: boolean;
  started_at: string;
  updated_at: string;
  messages: SpoolMessage[];
  metadata: SpoolMetadata;
}

/**
 * The subset of OpenCode types the plugin reads. The real SDK types are
 * Effect-branded and generated; keeping this structural copy makes the pure
 * normalizer testable without pulling the whole client into unit tests.
 */
export interface SessionInfoLike {
  readonly id: string;
  readonly projectID?: string;
  readonly title?: string;
  readonly agent?: string;
  readonly model?: { readonly providerID: string; readonly id: string };
  readonly time: { readonly created: number; readonly updated: number };
  readonly location?: { readonly directory: string };
}

export type AssistantContentLike =
  | { readonly type: 'text'; readonly text: string }
  | { readonly type: 'reasoning'; readonly text?: string }
  | {
      readonly type: 'tool';
      readonly name: string;
      readonly state?: { readonly status?: string; readonly input?: Record<string, unknown> };
    };

export interface SessionMessageLike {
  readonly id: string;
  readonly type: string;
  readonly time?: { readonly created?: number; readonly completed?: number };
  readonly text?: string;
  readonly content?: readonly AssistantContentLike[];
}

export interface NormalizeLimits {
  readonly maxMessageChars: number;
  readonly maxMessages: number;
  readonly maxToolCallsPerMessage: number;
  readonly maxToolFieldChars: number;
}

export interface NormalizeInput {
  readonly bundleId: string;
  readonly session: SessionInfoLike;
  readonly messages: readonly SessionMessageLike[];
  readonly final: boolean;
  readonly projectKey: string;
  readonly projectPath: string;
  readonly branch?: string;
  readonly opencodeVersion?: string;
  readonly limits?: Partial<NormalizeLimits>;
}
