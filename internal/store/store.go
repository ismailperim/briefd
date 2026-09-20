// Package store owns every SQL statement in briefd. It wraps a single SQLite
// database (WAL mode) accessed through the cgo-free ncruces driver (ADR-0002).
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store is a handle to the briefd database. It is safe for concurrent use.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens (creating if necessary) the database at path and applies any
// pending migrations. Use ":memory:" for an ephemeral in-process database.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?" + url.Values{
		"_pragma": {
			"journal_mode(WAL)",
			"busy_timeout(5000)",
			"foreign_keys(ON)",
			"synchronous(NORMAL)",
		},
		"_txlock": {"immediate"},
	}.Encode()
	if path == ":memory:" {
		dsn = "file::memory:?_pragma=foreign_keys(ON)"
	}
	db, err := driver.Open(dsn, fts5.Register)
	if err != nil {
		return nil, fmt.Errorf("opening database %s: %w", path, err)
	}
	if path == ":memory:" {
		// Every connection would otherwise get its own empty database.
		db.SetMaxOpenConns(1)
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// migrate applies embedded migrations in order, tracking progress with
// PRAGMA user_version. Migrations are named NNNN_name.sql.
func (s *Store) migrate(ctx context.Context) error {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("reading migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var current int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	for _, name := range names {
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %s: bad version prefix", name)
		}
		if version <= current {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", name, err)
		}
		if err := s.applyMigration(ctx, name, string(body), version); err != nil {
			return err
		}
		current = version
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, name, body string, version int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migration %s: %w", name, err)
	}
	defer tx.Rollback() // no-op after commit
	if _, err := tx.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("applying migration %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return fmt.Errorf("migration %s: setting version: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration %s: %w", name, err)
	}
	return nil
}

// isConstraint reports whether err is a SQLite constraint violation.
func isConstraint(err error) bool {
	var serr *sqlite3.Error
	return errors.As(err, &serr) && serr.Code() == sqlite3.CONSTRAINT
}
