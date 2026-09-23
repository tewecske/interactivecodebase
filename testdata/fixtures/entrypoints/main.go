// Command entries has entry points other than HTTP routes: cobra
// commands, a worker with a job table, a goroutine consuming events, a
// gRPC service and NATS subscriptions.
package main

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	"example.com/entries/notespb"
	"example.com/entries/worker"
)

func main() {
	var db *sql.DB
	root := &cobra.Command{Use: "entries", Short: "entry point fixture"}
	root.AddCommand(&cobra.Command{
		Use:   "migrate",
		Short: "apply migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			return migrate(cmd.Context(), db)
		},
	})
	root.AddCommand(newServeCmd(db))
	if err := root.Execute(); err != nil {
		log.Fatal(err)
	}
}

func newServeCmd(db *sql.DB) *cobra.Command {
	return &cobra.Command{
		Use:   "serve [addr]",
		Short: "run the workers and the gRPC server",
		Run: func(cmd *cobra.Command, args []string) {
			serve(context.Background(), db)
		},
	}
}

func migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS events (id BIGSERIAL PRIMARY KEY, kind TEXT NOT NULL)")
	return err
}

type store struct{ db *sql.DB }

func (s *store) purgeSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < $1", time.Now().Unix())
	return err
}

// reindex returns the job that marks notes as indexed.
func reindex(db *sql.DB) func(context.Context) error {
	return func(ctx context.Context) error {
		_, err := db.ExecContext(ctx, "UPDATE notes SET indexed = true WHERE NOT indexed")
		return err
	}
}

func serve(ctx context.Context, db *sql.DB) {
	s := &store{db: db}
	w := worker.New(time.Minute,
		worker.Job{Name: "purge-sessions", Run: s.purgeSessions},
		worker.Job{Name: "reindex-notes", Run: reindex(db)},
	)
	go w.Run(ctx)
	go func() {
		if err := consumeEvents(ctx, db); err != nil {
			log.Print(err)
		}
	}()

	srv := grpc.NewServer()
	notespb.RegisterNotesServer(srv, &notesServer{db: db})

	nc, err := nats.Connect("nats://localhost")
	if err != nil {
		return
	}
	_, _ = nc.Subscribe("notes.created", func(m *nats.Msg) {
		_, _ = db.ExecContext(ctx, "INSERT INTO events (kind) VALUES ('created')")
	})
	_, _ = nc.QueueSubscribe("notes.deleted", "indexers", s.onDeleted)
}

func (s *store) onDeleted(m *nats.Msg) {
	_, _ = s.db.Exec("DELETE FROM notes WHERE id = $1", string(m.Data))
}

func consumeEvents(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "SELECT id, kind FROM events")
	if err != nil {
		return err
	}
	return rows.Close()
}

type notesServer struct {
	notespb.UnimplementedNotesServer
	db *sql.DB
}

func (n *notesServer) GetNote(ctx context.Context, req *notespb.GetNoteRequest) (*notespb.Note, error) {
	note := &notespb.Note{Id: req.Id}
	err := n.db.QueryRowContext(ctx, "SELECT body FROM notes WHERE id = $1", req.Id).Scan(&note.Body)
	return note, err
}

func (n *notesServer) ListNotes(ctx context.Context, _ *notespb.ListNotesRequest) (*notespb.ListNotesResponse, error) {
	rows, err := n.db.QueryContext(ctx, "SELECT id, body FROM notes")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return &notespb.ListNotesResponse{}, nil
}
