CREATE TABLE notes (
    id         BIGSERIAL PRIMARY KEY,
    owner_id   BIGINT NOT NULL,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX notes_owner_created ON notes (owner_id, created_at DESC);

CREATE TABLE audit_events (
    id      BIGSERIAL PRIMARY KEY,
    note_id BIGINT,
    action  TEXT NOT NULL
);
