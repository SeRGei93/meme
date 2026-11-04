import assert from 'node:assert/strict';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { findGitRemote, resolveProjectKey } from '../project.ts';

test('prefers a normalized git remote over the project id', () => {
  for (const remoteUrl of [
    'git@github.com:SeRGei93/meme.git',
    'https://github.com/SeRGei93/meme.git',
    'https://github.com/SeRGei93/meme',
    'ssh://git@github.com:22/SeRGei93/meme.git',
    'git://github.com/SeRGei93/meme.git',
  ]) {
    assert.equal(
      resolveProjectKey({ remoteUrl, projectID: 'prj_1', directory: '/repo', hostname: 'mac' }),
      'github.com/SeRGei93/meme',
      remoteUrl,
    );
  }
});

test('falls back to opencode:<projectID> without a remote', () => {
  assert.equal(
    resolveProjectKey({ projectID: 'prj_abc', directory: '/repo', hostname: 'mac' }),
    'opencode:prj_abc',
  );
});

test('falls back to local:<host>:<path> when there is no remote and no project id', () => {
  assert.equal(resolveProjectKey({ directory: '/repo', hostname: 'mac' }), 'local:mac:/repo');
});

test('ignores blank remotes and keeps the remaining fallbacks', () => {
  assert.equal(
    resolveProjectKey({ remoteUrl: '   ', projectID: 'prj_abc', directory: '/repo', hostname: 'mac' }),
    'opencode:prj_abc',
  );
});

test('findGitRemote reads origin from a repository .git/config', async () => {
  const root = await mkdtemp(join(tmpdir(), 'sb-git-'));
  try {
    await mkdir(join(root, '.git'), { recursive: true });
    await writeFile(
      join(root, '.git', 'config'),
      '[core]\n\trepositoryformatversion = 0\n[remote "upstream"]\n\turl = https://gitlab.com/other/repo.git\n[remote "origin"]\n\turl = git@github.com:SeRGei93/meme.git\n',
    );
    assert.equal(await findGitRemote(join(root, 'nested', 'dir')), 'git@github.com:SeRGei93/meme.git');
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('findGitRemote supports a .git file pointing at a worktree', async () => {
  const root = await mkdtemp(join(tmpdir(), 'sb-git-'));
  try {
    const worktree = join(root, 'worktree');
    const commonDir = join(root, 'main', '.git');
    const gitDir = join(commonDir, 'worktrees', 'task');
    await mkdir(worktree, { recursive: true });
    await mkdir(gitDir, { recursive: true });
    await writeFile(join(worktree, '.git'), `gitdir: ${gitDir}\n`);
    await writeFile(join(gitDir, 'commondir'), '../..\n');
    await writeFile(join(commonDir, 'config'), '[remote "origin"]\n\turl = https://github.com/acme/worktree.git\n');
    assert.equal(await findGitRemote(worktree), 'https://github.com/acme/worktree.git');
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('findGitRemote returns undefined outside a repository', async () => {
  const root = await mkdtemp(join(tmpdir(), 'sb-nogit-'));
  try {
    assert.equal(await findGitRemote(join(root, 'nested')), undefined);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
