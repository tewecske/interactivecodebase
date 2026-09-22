// Package export writes note exports to disk.
package export

import (
	"context"
	"encoding/csv"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"example.com/webapp/internal/notes"
)

// WriteCSV writes notes to dir/name.csv and compresses it with gzip.
func WriteCSV(ctx context.Context, dir, name string, list []notes.Note) (string, error) {
	path := filepath.Join(dir, name+".csv")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	w := csv.NewWriter(f)
	for _, n := range list {
		if err := w.Write([]string{strconv.FormatInt(n.ID, 10), n.Title}); err != nil {
			f.Close()
			return "", err
		}
	}
	w.Flush()
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := exec.CommandContext(ctx, "gzip", "-f", path).Run(); err != nil {
		return "", err
	}
	return path + ".gz", nil
}
