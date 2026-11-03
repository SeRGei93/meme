CREATE TABLE clients (
    id            INTEGER PRIMARY KEY,
    name          TEXT NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('opencode', 'jarvis', 'ui', 'admin')),
    token_hash    TEXT NOT NULL UNIQUE,
    scopes        TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    last_seen_at  TEXT,
    revoked_at    TEXT
);

-- Client names are unique among active clients: a revoked name can be reused,
-- as required by the v1 contract for token rotation.
CREATE UNIQUE INDEX clients_active_name ON clients(name) WHERE revoked_at IS NULL;
