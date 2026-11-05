# Clients and sources are generic

Second Brain is memory for any agent with a token, not only OpenCode and Jarvis. A **Client** is the unit of access: it has a unique, immutable **Source** chosen when the token is issued (defaulting to a slug of its name), scopes, and a revocation state. Source comes from the client record, never from the request body or from a closed `kind` enum. Only an admin token can issue clients; there is no open registration or self-service token exchange.

MCP is the primary integration (`remember`, `search` and the other read tools). `POST /api/v1/memories` remains the HTTP fallback, while `/api/v1/ingest` keeps the OpenCode spool-bundle format: any agent with the `ingest` scope may post one, but OpenCode is the producer. OpenCode and Jarvis become ordinary clients; the migration backfills historical record sources from their client and removes the fixed `opencode`/`jarvis` source constraint.

**Considered options**: family-level sources shared by many clients (rejected: filtering could not tell machines apart), source derived from client kind (rejected: forced every new agent into a code change), self-service tokens (rejected: anyone could write into memory).

**Consequences**: search, stats and the graph must accept arbitrary source strings; the app cannot use a fixed two-value source enum; token issuance needs an admin API/CLI and an out-of-band delivery path for remote agents such as Jarvis.
