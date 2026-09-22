// Package notes holds the note use cases and the ports they consume.
package notes

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ErrEmptyTitle is returned when a note has no title.
var ErrEmptyTitle = errors.New("notes: empty title")

// Note is a user's note.
type Note struct {
	ID        int64
	OwnerID   int64
	Title     string
	Body      string
	CreatedAt time.Time
}

// Repository persists notes.
type Repository interface {
	Create(ctx context.Context, note Note) (Note, error)
	ListByOwner(ctx context.Context, ownerID int64) ([]Note, error)
	Get(ctx context.Context, id int64) (Note, error)
}

// Mailer delivers mail.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// Service implements note use cases.
type Service struct {
	repo Repository
	mail Mailer
}

// NewService returns a Service over repo and mail.
func NewService(repo Repository, mail Mailer) *Service {
	return &Service{repo: repo, mail: mail}
}

// Create validates and stores a new note.
func (s *Service) Create(ctx context.Context, ownerID int64, title, body string) (Note, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Note{}, ErrEmptyTitle
	}
	return s.repo.Create(ctx, Note{OwnerID: ownerID, Title: title, Body: body, CreatedAt: time.Now()})
}

// List returns the owner's notes.
func (s *Service) List(ctx context.Context, ownerID int64) ([]Note, error) {
	return s.repo.ListByOwner(ctx, ownerID)
}

// Get returns one note.
func (s *Service) Get(ctx context.Context, id int64) (Note, error) {
	return s.repo.Get(ctx, id)
}

// Share mails a note to an address.
func (s *Service) Share(ctx context.Context, id int64, to string) error {
	note, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	return s.mail.Send(ctx, to, note.Title, note.Body)
}
