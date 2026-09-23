-- name: GetUser :one
SELECT id, email, name FROM users WHERE id = $1;

-- name: ListNotes :many
SELECT id, user_id, body FROM notes WHERE user_id = $1;

-- name: CreateNote :exec
INSERT INTO notes (user_id, body) VALUES ($1, $2);
