CREATE TABLE projects (
    id          INTEGER PRIMARY KEY,
    key         TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL,
    path        TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
);

CREATE TABLE records (
    id              INTEGER PRIMARY KEY,
    client_id       INTEGER NOT NULL REFERENCES clients(id),
    source          TEXT NOT NULL,
    external_id     TEXT NOT NULL,
    project_id      INTEGER REFERENCES projects(id),
    kind            TEXT NOT NULL DEFAULT 'session',
    title           TEXT NOT NULL,
    summary         TEXT NOT NULL DEFAULT '',
    questions       TEXT NOT NULL DEFAULT '',
    keywords        TEXT NOT NULL DEFAULT '',
    atoms           TEXT NOT NULL DEFAULT '',
    raw_text        TEXT NOT NULL,
    content_hash    TEXT NOT NULL,
    revision        INTEGER NOT NULL DEFAULT 1,
    is_final        INTEGER NOT NULL DEFAULT 0,
    finalized_at    TEXT,
    started_at      TEXT,
    updated_at      TEXT NOT NULL,
    indexed_at      TEXT NOT NULL,
    card_model      TEXT,
    card_status     TEXT NOT NULL DEFAULT 'heuristic',
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    UNIQUE(client_id, source, external_id)
);

CREATE INDEX idx_records_project ON records(project_id);
CREATE INDEX idx_records_updated ON records(updated_at DESC);
CREATE INDEX idx_records_final   ON records(is_final, updated_at);

CREATE VIRTUAL TABLE records_fts USING fts5(
    title,
    summary,
    questions,
    keywords,
    atoms,
    raw_text,
    content='records',
    content_rowid='id',
    tokenize='unicode61 remove_diacritics 2'
);

CREATE TRIGGER records_ai AFTER INSERT ON records BEGIN
    INSERT INTO records_fts(rowid, title, summary, questions, keywords, atoms, raw_text)
    VALUES (new.id, new.title, new.summary, new.questions, new.keywords, new.atoms, new.raw_text);
END;

CREATE TRIGGER records_ad AFTER DELETE ON records BEGIN
    INSERT INTO records_fts(records_fts, rowid, title, summary, questions, keywords, atoms, raw_text)
    VALUES ('delete', old.id, old.title, old.summary, old.questions, old.keywords, old.atoms, old.raw_text);
END;

CREATE TRIGGER records_au AFTER UPDATE ON records BEGIN
    INSERT INTO records_fts(records_fts, rowid, title, summary, questions, keywords, atoms, raw_text)
    VALUES ('delete', old.id, old.title, old.summary, old.questions, old.keywords, old.atoms, old.raw_text);

    INSERT INTO records_fts(rowid, title, summary, questions, keywords, atoms, raw_text)
    VALUES (new.id, new.title, new.summary, new.questions, new.keywords, new.atoms, new.raw_text);
END;

CREATE TABLE ingest_jobs (
    id               INTEGER PRIMARY KEY,
    client_id        INTEGER NOT NULL REFERENCES clients(id),
    idempotency_key  TEXT NOT NULL DEFAULT '',
    source           TEXT NOT NULL,
    external_id      TEXT NOT NULL,
    record_id        INTEGER REFERENCES records(id),
    kind             TEXT NOT NULL,
    status           TEXT NOT NULL,
    attempts         INTEGER NOT NULL DEFAULT 0,
    next_retry_at    TEXT,
    error            TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE INDEX idx_ingest_jobs_status ON ingest_jobs(status, id);

-- Idempotency keys are tracked separately from the queue: a request can be
-- replayed without creating a new card-generation job. Rows are valid for 24h,
-- after which a later request with the same key replaces the row.
CREATE TABLE idempotency_keys (
    client_id     INTEGER NOT NULL REFERENCES clients(id),
    operation     TEXT NOT NULL,
    key           TEXT NOT NULL,
    request_hash  TEXT NOT NULL,
    record_id     INTEGER NOT NULL REFERENCES records(id),
    job_id        INTEGER REFERENCES ingest_jobs(id),
    status        TEXT NOT NULL CHECK (status IN ('queued', 'stored')),
    created_at    TEXT NOT NULL,
    PRIMARY KEY (client_id, operation, key)
);

CREATE INDEX idx_idempotency_created ON idempotency_keys(created_at);
