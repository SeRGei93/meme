# MCP first, OpenCode auto-capture opt-in

MCP is the default integration: the agent decides what to remember, or the user asks it explicitly. The OpenCode plugin is an optional session journal, not the primary way memory is written. On a new install the plugin is not installed; onboarding and Settings expose one toggle to enable automatic capture. Enabling it installs the app-owned plugin, disabling it removes only that plugin. The MCP entry in `opencode.json` is configured in both cases.

Auto-capture writes a bundle on `session.idle`, compaction, failure, interruption and backfills existing history. That is valuable for users who want a complete journal, but it turns memory into a log of drafts and debugging. MCP `remember` with `title` and `summary` also bypasses the card LLM, producing more predictable cards and fewer keywords.

**Considered options**: keeping auto-capture on by default (rejected: too much noise), deleting the plugin (rejected: loses the guaranteed journal and backfill for users who want it), a plugin-side capture filter (rejected: the app already owns installation and can make the choice explicit).

**Consequences**: onboarding still issues the OpenCode client and writes `mcp_token` for MCP, but does not install the plugin unless enabled. `config.json` records `auto_capture`; the installed plugin state remains the source of truth for the UI. Turning auto-capture off must not break MCP, tokens or uploads.
