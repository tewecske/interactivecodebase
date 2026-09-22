// Package postgres implements persistence ports with database/sql.
package postgres

import (
	"context"
	"database/sql"

	"example.com/webapp/internal/notes"
)

// NoteRepository stores notes in PostgreSQL.
type NoteRepository struct {
	db *sql.DB
}

var _ notes.Repository = (*NoteRepository)(nil)

// NewNoteRepository returns a NoteRepository over db.
func NewNoteRepository(db *sql.DB) *NoteRepository {
	return &NoteRepository{db: db}
}

const noteSelect = `SELECT id, owner_id, title, body, created_at FROM notes`

// Create inserts a note and records an audit event in one transaction.
func (r *NoteRepository) Create(ctx context.Context, note notes.Note) (notes.Note, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return notes.Note{}, err
	}
	defer func() { _ = tx.Rollback() }()
	err = tx.QueryRowContext(ctx,
		`INSERT INTO notes (owner_id, title, body, created_at) VALUES ($1, $2, $3, $4) RETURNING id`,
		note.OwnerID, note.Title, note.Body, note.CreatedAt,
	).Scan(&note.ID)
	if err != nil {
		return notes.Note{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO audit_events (note_id, action) VALUES ($1, 'create')`, note.ID,
	); err != nil {
		return notes.Note{}, err
	}
	return note, tx.Commit()
}

// ListByOwner returns an owner's notes, newest first.
func (r *NoteRepository) ListByOwner(ctx context.Context, ownerID int64) ([]notes.Note, error) {
	rows, err := r.db.QueryContext(ctx, noteSelect+" WHERE owner_id = $1 ORDER BY created_at DESC", ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []notes.Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Get returns one note by ID.
func (r *NoteRepository) Get(ctx context.Context, id int64) (notes.Note, error) {
	return scanNote(r.db.QueryRowContext(ctx, noteSelect+" WHERE id = $1", id))
}

type scanner interface{ Scan(dest ...any) error }

func scanNote(s scanner) (notes.Note, error) {
	var n notes.Note
	err := s.Scan(&n.ID, &n.OwnerID, &n.Title, &n.Body, &n.CreatedAt)
	return n, err
}
