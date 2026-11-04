-- Memories reuse idempotency_keys with the response status `indexed`, which the
-- original CHECK constraint did not allow. SQLite cannot alter a CHECK
-- constraint in place, so the table is rebuilt.
CREATE TABLE idempotency_keys_new (
    client_id     INTEGER NOT NULL REFERENCES clients(id),
    operation     TEXT NOT NULL,
    key           TEXT NOT NULL,
    request_hash  TEXT NOT NULL,
    record_id     INTEGER NOT NULL REFERENCES records(id),
    job_id        INTEGER REFERENCES ingest_jobs(id),
    status        TEXT NOT NULL CHECK (status IN ('queued', 'stored', 'indexed')),
    created_at    TEXT NOT NULL,
    PRIMARY KEY (client_id, operation, key)
);

INSERT INTO idempotency_keys_new
    (client_id, operation, key, request_hash, record_id, job_id, status, created_at)
SELECT client_id, operation, key, request_hash, record_id, job_id, status, created_at
FROM idempotency_keys;

DROP TABLE idempotency_keys;

ALTER TABLE idempotency_keys_new RENAME TO idempotency_keys;

CREATE INDEX idx_idempotency_created ON idempotency_keys(created_at);
