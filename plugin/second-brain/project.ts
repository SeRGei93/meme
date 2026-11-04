import { readFile, stat } from 'node:fs/promises';
import { dirname, isAbsolute, join, resolve } from 'node:path';

export interface ProjectKeyInput {
  readonly remoteUrl?: string;
  readonly projectID?: string;
  readonly directory: string;
  readonly hostname: string;
}

/**
 * project_key priority from docs/spool.schema.json: git remote as
 * host/owner/repo, then opencode:<projectID>, then local:<host>:<path>.
 */
export function resolveProjectKey(input: ProjectKeyInput): string {
  const remote = input.remoteUrl ? normalizeRemote(input.remoteUrl) : undefined;
  if (remote) return remote;
  if (input.projectID) return `opencode:${input.projectID}`;
  return `local:${input.hostname}:${input.directory}`;
}

/**
 * Normalizes the common git remote spellings:
 *   git@github.com:Owner/Repo.git       -> github.com/Owner/Repo
 *   ssh://git@github.com:22/Owner/Repo  -> github.com/Owner/Repo
 *   https://github.com/Owner/Repo.git   -> github.com/Owner/Repo
 * The host is lowercased; the owner/repo path keeps its case because it is
 * the project key users see.
 */
export function normalizeRemote(remoteUrl: string): string | undefined {
  const value = remoteUrl.trim();
  if (value === '') return undefined;

  const scpLike = /^(?:[^@/\s]+@)?([^:/\s]+):(?!\/\/)(.+)$/.exec(value);
  if (scpLike && !value.includes('://')) {
    return cleanHostPath(scpLike[1], scpLike[2]);
  }

  try {
    const url = new URL(value);
    return cleanHostPath(url.hostname, url.pathname);
  } catch {
    return undefined;
  }
}

function cleanHostPath(host: string | undefined, path: string | undefined): string | undefined {
  if (!host || !path) return undefined;
  let repo = path.replace(/^\/+/, '').replace(/\/+$/, '');
  repo = repo.replace(/\.git$/i, '');
  if (repo === '') return undefined;
  return `${host.toLowerCase()}/${repo}`;
}

/**
 * Finds the origin remote by walking up from `directory` and reading the
 * repository config locally. No git binary and no network are involved.
 */
export async function findGitRemote(directory: string): Promise<string | undefined> {
  let current = resolve(directory);
  for (;;) {
    const configPath = await configPathFor(join(current, '.git'));
    if (configPath) {
      const remote = await readOrigin(configPath);
      if (remote) return remote;
    }
    const parent = dirname(current);
    if (parent === current) return undefined;
    current = parent;
  }
}

async function configPathFor(gitPath: string): Promise<string | undefined> {
  try {
    const info = await stat(gitPath);
    if (info.isDirectory()) return join(gitPath, 'config');
    if (!info.isFile()) return undefined;
  } catch {
    return undefined;
  }

  // Worktrees use a .git file: "gitdir: <path>". Its config may live next to
  // the gitdir or in the common dir referenced by the commondir file.
  let contents: string;
  try {
    contents = await readFile(gitPath, 'utf8');
  } catch {
    return undefined;
  }
  const match = /^gitdir:\s*(.+)$/m.exec(contents);
  if (!match?.[1]) return undefined;
  const gitDir = isAbsolute(match[1].trim()) ? match[1].trim() : resolve(dirname(gitPath), match[1].trim());

  const direct = join(gitDir, 'config');
  if (await isFile(direct)) return direct;

  try {
    const common = (await readFile(join(gitDir, 'commondir'), 'utf8')).trim();
    const commonDir = isAbsolute(common) ? common : resolve(gitDir, common);
    const commonConfig = join(commonDir, 'config');
    if (await isFile(commonConfig)) return commonConfig;
  } catch {
    // fall through
  }
  return undefined;
}

async function isFile(path: string): Promise<boolean> {
  try {
    return (await stat(path)).isFile();
  } catch {
    return false;
  }
}

async function readOrigin(configPath: string): Promise<string | undefined> {
  let contents: string;
  try {
    contents = await readFile(configPath, 'utf8');
  } catch {
    return undefined;
  }
  const remotes = new Map<string, string>();
  let current: string | undefined;
  for (const line of contents.split('\n')) {
    const section = /^\s*\[remote\s+"([^"]+)"\]\s*$/.exec(line);
    if (section?.[1]) {
      current = section[1];
      continue;
    }
    if (/^\s*\[/.test(line)) {
      current = undefined;
      continue;
    }
    if (!current) continue;
    const url = /^\s*url\s*=\s*(.+?)\s*$/.exec(line);
    if (url?.[1] && !remotes.has(current)) remotes.set(current, url[1]);
  }
  return remotes.get('origin') ?? remotes.values().next().value;
}
