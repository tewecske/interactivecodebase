CREATE TABLE users (
    id       BIGSERIAL PRIMARY KEY,
    email    TEXT NOT NULL UNIQUE,
    is_admin BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);
