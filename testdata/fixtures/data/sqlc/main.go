// Command sqlcapp uses sqlc-generated queries. Nothing in the program
// creates the DBTX, so only the generated query text says what runs.
package main

import (
	"context"

	"example.com/sqlcapp/db"
)

// Serve is handed the database by the caller.
func Serve(ctx context.Context, conn db.DBTX) error {
	q := db.New(conn)
	u, err := q.GetUser(ctx, 1)
	if err != nil {
		return err
	}
	if _, err := q.ListNotes(ctx, u.ID); err != nil {
		return err
	}
	return q.CreateNote(ctx, db.CreateNoteParams{UserID: u.ID, Body: "hi"})
}

func main() {}
