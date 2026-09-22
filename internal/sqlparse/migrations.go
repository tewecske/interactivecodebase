package sqlparse

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DefaultMigrationDirs are searched, relative to the module root, when no
// directories are configured.
var DefaultMigrationDirs = []string{"migrations", "db/migrations", "sql/migrations", "internal/migrations", "schema"}

// LoadMigrations builds the schema by applying the migration files in dirs
// (relative to root) in name order. Up migrations are "*.up.sql"; if a
// directory has none, every "*.sql" except "*.down.sql" is used, taking
// only the "-- +goose Up" section of goose files. File positions are
// relative to root.
func LoadMigrations(root string, dirs []string) (*Schema, []string, error) {
	if dirs == nil {
		dirs = DefaultMigrationDirs
	}
	s := &Schema{}
	var used []string
	for _, dir := range dirs {
		files, err := migrationFiles(filepath.Join(root, dir))
		if err != nil {
			return nil, nil, err
		}
		for _, file := range files {
			b, err := os.ReadFile(file)
			if err != nil {
				return nil, nil, err
			}
			rel, err := filepath.Rel(root, file)
			if err != nil {
				rel = file
			}
			s.Apply(filepath.ToSlash(rel), gooseUp(string(b)))
		}
		if len(files) > 0 {
			used = append(used, dir)
		}
	}
	return s, used, nil
}

func migrationFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var up, all []string
	for _, e := range entries {
		name := e.Name()
		if e.Type()&fs.ModeType != 0 || !strings.HasSuffix(name, ".sql") {
			continue
		}
		switch {
		case strings.HasSuffix(name, ".up.sql"):
			up = append(up, filepath.Join(dir, name))
		case !strings.HasSuffix(name, ".down.sql"):
			all = append(all, filepath.Join(dir, name))
		}
	}
	files := up
	if len(files) == 0 {
		files = all
	}
	slices.Sort(files)
	return files, nil
}

// gooseUp returns the Up section of a goose migration, keeping line
// numbers by blanking the Down section; other files are returned unchanged.
func gooseUp(sql string) string {
	if !strings.Contains(sql, "+goose Up") {
		return sql
	}
	lines := strings.Split(sql, "\n")
	down := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "-- +goose Down"):
			down = true
		case strings.HasPrefix(trimmed, "-- +goose Up"):
			down = false
		}
		if down || strings.HasPrefix(trimmed, "-- +goose") {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}
