// Package sqlindex implements ports.Index over a SQLite database file. The
// index is a derived cache of the working tree, never authoritative: every
// value it holds is reconstructible from Docs, and Reset can always empty it
// without losing anything git does not already have.
package sqlindex

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/tunedev/atlas/internal/core/ports"
)

const schema = `
CREATE TABLE IF NOT EXISTS records (
    path TEXT PRIMARY KEY,
    rev  TEXT NOT NULL,
    kind TEXT NOT NULL,
    when_utc INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS fields (
    path  TEXT NOT NULL REFERENCES records(path) ON DELETE CASCADE,
    name  TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY (path, name)
);
CREATE INDEX IF NOT EXISTS records_kind_idx ON records (kind);
CREATE INDEX IF NOT EXISTS fields_lookup_idx ON fields (name, value);
`

// Index implements ports.Index over a SQLite database file.
type Index struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and ensures
// its schema exists.
func Open(ctx context.Context, path string) (*Index, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sqlindex: open %s: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlindex: enable foreign keys: %w", err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlindex: create schema: %w", err)
	}
	return &Index{db: db}, nil
}

// PathKeyed marks Index as holding one row per path, keyed on path alone.
// internal/core/app uses it to accept this index only where a current-state
// rebuild walk is correct.
func (idx *Index) PathKeyed() {}

// Close releases the underlying database handle.
func (idx *Index) Close() error {
	if err := idx.db.Close(); err != nil {
		return fmt.Errorf("sqlindex: close: %w", err)
	}
	return nil
}

// Upsert writes r's record and field rows in one transaction. The path's
// existing fields are deleted before the new ones are written, so a field r
// no longer sets does not survive.
func (idx *Index) Upsert(ctx context.Context, r ports.Record) error {
	tx, err := idx.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlindex: begin upsert %s: %w", r.Path, err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO records (path, rev, kind, when_utc) VALUES (?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET rev = excluded.rev, kind = excluded.kind, when_utc = excluded.when_utc
	`, r.Path, string(r.Rev), r.Kind, r.When.UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("sqlindex: upsert record %s: %w", r.Path, err)
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM fields WHERE path = ?", r.Path); err != nil {
		return fmt.Errorf("sqlindex: clear fields for %s: %w", r.Path, err)
	}
	for name, value := range r.Fields {
		if _, err := tx.ExecContext(ctx, "INSERT INTO fields (path, name, value) VALUES (?, ?, ?)", r.Path, name, value); err != nil {
			return fmt.Errorf("sqlindex: insert field %s for %s: %w", name, r.Path, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlindex: commit upsert %s: %w", r.Path, err)
	}
	return nil
}

// Find returns every record of q.Kind whose fields match every entry in
// q.Match. An empty q.Match matches every record of the kind. Every value in
// q.Match is bound as a query parameter, never interpolated into the SQL
// text.
func (idx *Index) Find(ctx context.Context, q ports.Query) ([]ports.Record, error) {
	var b strings.Builder
	args := []any{q.Kind}
	b.WriteString("SELECT path, rev, kind, when_utc FROM records WHERE kind = ?")
	for name, value := range q.Match {
		b.WriteString(" AND path IN (SELECT path FROM fields WHERE name = ? AND value = ?)")
		args = append(args, name, value)
	}
	if q.Limit > 0 {
		b.WriteString(" LIMIT ?")
		args = append(args, q.Limit)
	}

	rows, err := idx.db.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("sqlindex: find: %w", err)
	}
	defer rows.Close()

	var records []ports.Record
	for rows.Next() {
		var (
			path, rev, kind string
			whenNano        int64
		)
		if err := rows.Scan(&path, &rev, &kind, &whenNano); err != nil {
			return nil, fmt.Errorf("sqlindex: scan record: %w", err)
		}
		records = append(records, ports.Record{
			Path: path,
			Rev:  ports.Revision(rev),
			Kind: kind,
			When: time.Unix(0, whenNano).UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlindex: find: %w", err)
	}

	for i := range records {
		fields, err := idx.fieldsFor(ctx, records[i].Path)
		if err != nil {
			return nil, err
		}
		records[i].Fields = fields
	}
	return records, nil
}

// fieldsFor returns the field map for path.
func (idx *Index) fieldsFor(ctx context.Context, path string) (map[string]string, error) {
	rows, err := idx.db.QueryContext(ctx, "SELECT name, value FROM fields WHERE path = ?", path)
	if err != nil {
		return nil, fmt.Errorf("sqlindex: fields for %s: %w", path, err)
	}
	defer rows.Close()

	fields := map[string]string{}
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return nil, fmt.Errorf("sqlindex: scan field for %s: %w", path, err)
		}
		fields[name] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlindex: fields for %s: %w", path, err)
	}
	return fields, nil
}

// Reset deletes every row from both tables without dropping them, so the
// schema survives.
func (idx *Index) Reset(ctx context.Context) error {
	tx, err := idx.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlindex: begin reset: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "DELETE FROM fields"); err != nil {
		return fmt.Errorf("sqlindex: reset fields: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM records"); err != nil {
		return fmt.Errorf("sqlindex: reset records: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlindex: commit reset: %w", err)
	}
	return nil
}
