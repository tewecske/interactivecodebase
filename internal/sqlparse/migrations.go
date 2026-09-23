package sqlparse

import (
	"cmp"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// DefaultMigrationDirs are searched, relative to the module root, when no
// directories are configured.
var DefaultMigrationDirs = []string{"migrations", "db/migrations", "sql/migrations", "internal/migrations", "schema"}

// LoadMigrations builds the schema by applying the migration files in dirs
// (relative to root) in name order. Up migrations are "*.up.sql"; if a
// directory has none, every "*.sql" except "*.down.sql" is used, taking
// only the "-- +goose Up" section of goose files. A directory of Flyway
// migrations ("V2__words.sql") is applied in version order, repeatable
// ones ("R__views.sql") last and undo ones ("U2__words.sql") left out.
// File positions are relative to root.
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
	if flyway(files) {
		files = slices.DeleteFunc(files, func(f string) bool { return filepath.Base(f)[0] == 'U' })
		slices.SortStableFunc(files, compareFlyway)
	}
	return files, nil
}

// flywayName matches Flyway's versioned, undo and repeatable migrations:
// "V1__init.sql", "V1_2__more.sql", "U1__init.sql", "R__views.sql".
var flywayName = regexp.MustCompile(`^(?:[VU](\d+(?:[._]\d+)*)|R)__`)

func flyway(files []string) bool {
	return len(files) > 0 && !slices.ContainsFunc(files, func(f string) bool { return !flywayName.MatchString(filepath.Base(f)) })
}

// compareFlyway orders versioned migrations by version, then repeatable
// ones by name.
func compareFlyway(a, b string) int {
	va, vb := flywayVersion(a), flywayVersion(b)
	if (va == nil) != (vb == nil) {
		if va == nil {
			return 1
		}
		return -1
	}
	if c := slices.Compare(va, vb); c != 0 {
		return c
	}
	return cmp.Compare(filepath.Base(a), filepath.Base(b))
}

// flywayVersion is the version of a versioned migration, nil for a
// repeatable one.
func flywayVersion(file string) []int {
	m := flywayName.FindStringSubmatch(filepath.Base(file))
	if m == nil || m[1] == "" {
		return nil
	}
	var v []int
	for _, part := range strings.FieldsFunc(m[1], func(r rune) bool { return r == '.' || r == '_' }) {
		n, _ := strconv.Atoi(part)
		v = append(v, n)
	}
	return v
}

// FlywayDirs finds Flyway's migration directories under root: each
// src/main/resources/db/migration of an sbt, Maven or Gradle module, or
// the directories under it that hold the migrations (one per database,
// e.g. db/migration/postgresql), preferring a PostgreSQL one. Build output
// and hidden directories are skipped.
func FlywayDirs(root string) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path != root && os.IsPermission(err) {
				return fs.SkipDir
			}
			return err
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if path != root && (strings.HasPrefix(name, ".") || name == "target" || name == "node_modules" || name == "build" || name == "out") {
			return fs.SkipDir
		}
		if !strings.HasSuffix(filepath.ToSlash(path), "/src/main/resources/db/migration") {
			return nil
		}
		found, err := flywayDir(path)
		if err != nil {
			return err
		}
		for _, f := range found {
			if rel, err := filepath.Rel(root, f); err == nil {
				dirs = append(dirs, filepath.ToSlash(rel))
			}
		}
		return fs.SkipDir
	})
	return dirs, err
}

// flywayDir is dir if it holds migrations, else its subdirectories that
// do, or only the PostgreSQL one among them.
func flywayDir(dir string) ([]string, error) {
	files, err := migrationFiles(dir)
	if err != nil || len(files) > 0 {
		return []string{dir}, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var subs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		files, err := migrationFiles(sub)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			continue
		}
		if strings.HasPrefix(strings.ToLower(e.Name()), "postgres") {
			return []string{sub}, nil
		}
		subs = append(subs, sub)
	}
	return subs, nil
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
