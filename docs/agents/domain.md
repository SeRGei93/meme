# Domain Docs

How the engineering skills should consume this repo's domain documentation.

## Before exploring, read these

- `GLOSSARY.md` at the repo root, if it exists.
- `docs/adr/`: read ADRs that touch the area you are about to work in.

If these files do not exist, proceed silently. The `@domain-modeling` skill creates them lazily when terms or decisions actually get resolved.

## File structure

Single-context repo:

```text
/
├── GLOSSARY.md
├── docs/adr/
│   ├── 0001-remote-sqlite.md
│   └── 0002-spool-uploader.md
└── ...
```

## Use the glossary's vocabulary

When naming a domain concept in an issue, refactor, test, or ADR, use the term as defined in `GLOSSARY.md`. Do not drift to synonyms the glossary avoids.

If a concept is missing, note it for `/domain-modeling` rather than inventing new language.

## Flag ADR conflicts

If an output contradicts an existing ADR, surface it explicitly:

> _Contradicts ADR-0007, but worth reopening because…_
