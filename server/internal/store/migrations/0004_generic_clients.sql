-- Clients become generic: kind is a capability preset (agent/ui/admin) and the
-- source is a unique label chosen when the token is issued. Existing clients
-- keep a slug of their name as source; legacy opencode/jarvis kinds become
-- agent. The table is rebuilt because SQLite cannot alter a CHECK constraint.
--
-- Source slugging, uniqueness among active clients and the record backfill run
-- in Go after this file (migrateGenericClients): the collision rules need a
-- deterministic in-memory set, which SQL cannot guarantee.
CREATE TABLE clients_new (
    id            INTEGER PRIMARY KEY,
    name          TEXT NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('agent', 'ui', 'admin')),
    source        TEXT,
    token_hash    TEXT NOT NULL UNIQUE,
    scopes        TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    last_seen_at  TEXT,
    revoked_at    TEXT,
    CHECK ((kind = 'agent' AND source IS NOT NULL) OR (kind <> 'agent' AND source IS NULL))
);

INSERT INTO clients_new
    (id, name, kind, source, token_hash, scopes, created_at, last_seen_at, revoked_at)
SELECT
    id,
    name,
    CASE WHEN kind IN ('opencode', 'jarvis') THEN 'agent' ELSE kind END,
    CASE WHEN kind IN ('opencode', 'jarvis') THEN name ELSE NULL END,
    token_hash,
    scopes,
    created_at,
    last_seen_at,
    revoked_at
FROM clients;

DROP TABLE clients;

ALTER TABLE clients_new RENAME TO clients;

-- Client names are unique among active clients: a revoked name can be reused,
-- as required by the v1 contract for token rotation.
CREATE UNIQUE INDEX clients_active_name ON clients(name) WHERE revoked_at IS NULL;
