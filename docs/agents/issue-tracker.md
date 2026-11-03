# Issue tracker: GitHub

Issues and specs for this repo live as GitHub issues in `SeRGei93/meme`. Use the `gh` CLI for all operations.

## Conventions

- **Create an issue**: `gh issue create --title "..." --body "..."`. Use a heredoc for multi-line bodies.
- **Read an issue**: `gh issue view <number> --json number,title,body,labels,comments`.
- **List issues**: `gh issue list --state open --json number,title,body,labels,comments`.
- **Comment**: `gh issue comment <number> --body "..."`.
- **Labels**: `gh issue edit <number> --add-label "..."` / `--remove-label "..."`.
- **Close**: comment with the explanation, then `gh issue close <number>`.

Infer the repo from `git remote -v`; `gh` does this automatically inside a clone.

Never file into `mattpocock/skills` or any other upstream repo. Before the first `gh issue create` of a session, verify the target:

```bash
gh repo view --json nameWithOwner
```

It must be `SeRGei93/meme`.

## Blocking

GitHub's native issue dependencies are preferred. With `gh` 2.94+:

```bash
gh issue create --blocked-by <n>,<n> ...
gh issue edit <child> --add-blocked-by <blocker>
```

With older `gh`, put this line at the top of the child body:

```text
Blocked by: #<n>, #<n>
```

A ticket is unblocked when every blocker is closed.

## Sub-issues

With `gh` 2.94+:

```bash
gh issue create --parent <parent> ...
gh issue edit <parent> --add-sub-issue <child>
```

With older `gh`, put this line at the top of the child body:

```text
Part of #<parent>
```

## When a skill says "publish to the issue tracker"

Create a GitHub issue.

## When a skill says "fetch the relevant ticket"

Read the issue with `gh issue view`.

## Pull requests as a triage surface

**PRs as a request surface: no.**
