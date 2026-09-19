// Package duckindex implements ports.Index over a DuckDB database file. The
// index is a derived cache of the document history, never authoritative:
// every value it holds is reconstructible from Docs, and Reset can always
// empty it without losing anything git does not already have.
//
// Unlike sqlindex, which holds one row per path, this index holds one row
// per revision: the primary key is (path, rev), so re-upserting a path with
// a new revision adds a row instead of replacing one, and history can be
// scanned rather than only replayed.
package duckindex

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/marcboeker/go-duckdb/v2"

	"github.com/tunedev/atlas/internal/core/ports"
)

const schema = `
CREATE TABLE IF NOT EXISTS revisions (
    path TEXT NOT NULL,
    rev  TEXT NOT NULL,
    kind TEXT NOT NULL,
    when_utc BIGINT NOT NULL,
    PRIMARY KEY (path, rev)
);
CREATE TABLE IF NOT EXISTS fields (
    path  TEXT NOT NULL,
    rev   TEXT NOT NULL,
    name  TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY (path, rev, name)
);
`

// Index implements ports.Index over a DuckDB database file.
type Index struct {
	db *sql.DB
}

// Open opens (creating if needed) the DuckDB database at path and ensures
// its schema exists.
func Open(ctx context.Context, path string) (*Index, error) {
	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, fmt.Errorf("duckindex: open %s: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("duckindex: create schema: %w", err)
	}
	return &Index{db: db}, nil
}

// Close releases the underlying database handle.
func (idx *Index) Close() error {
	if err := idx.db.Close(); err != nil {
		return fmt.Errorf("duckindex: close: %w", err)
	}
	return nil
}

// Upsert writes r's revision and field rows in one transaction. DuckDB has
// no ON DELETE CASCADE, so the revision's existing fields are deleted
// explicitly before the new ones are written.
func (idx *Index) Upsert(ctx context.Context, r ports.Record) error {
	tx, err := idx.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("duckindex: begin upsert %s@%s: %w", r.Path, r.Rev, err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO revisions (path, rev, kind, when_utc) VALUES (?, ?, ?, ?)
		ON CONFLICT (path, rev) DO UPDATE SET kind = excluded.kind, when_utc = excluded.when_utc
	`, r.Path, string(r.Rev), r.Kind, r.When.UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("duckindex: upsert revision %s@%s: %w", r.Path, r.Rev, err)
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM fields WHERE path = ? AND rev = ?", r.Path, string(r.Rev)); err != nil {
		return fmt.Errorf("duckindex: clear fields for %s@%s: %w", r.Path, r.Rev, err)
	}
	for name, value := range r.Fields {
		_, err := tx.ExecContext(ctx, "INSERT INTO fields (path, rev, name, value) VALUES (?, ?, ?, ?)",
			r.Path, string(r.Rev), name, value)
		if err != nil {
			return fmt.Errorf("duckindex: insert field %s for %s@%s: %w", name, r.Path, r.Rev, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("duckindex: commit upsert %s@%s: %w", r.Path, r.Rev, err)
	}
	return nil
}

// Find returns every revision of q.Kind whose fields match every entry in
// q.Match. An empty q.Match matches every revision of the kind. Every value
// in q.Match is bound as a query parameter, never interpolated into the SQL
// text.
func (idx *Index) Find(ctx context.Context, q ports.Query) ([]ports.Record, error) {
	var b strings.Builder
	args := []any{q.Kind}
	b.WriteString("SELECT path, rev, kind, when_utc FROM revisions WHERE kind = ?")
	for name, value := range q.Match {
		b.WriteString(" AND (path, rev) IN (SELECT path, rev FROM fields WHERE name = ? AND value = ?)")
		args = append(args, name, value)
	}
	if q.Limit > 0 {
		b.WriteString(" LIMIT ?")
		args = append(args, q.Limit)
	}

	rows, err := idx.db.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("duckindex: find: %w", err)
	}
	defer rows.Close()

	var records []ports.Record
	for rows.Next() {
		var (
			path, rev, kind string
			whenNano        int64
		)
		if err := rows.Scan(&path, &rev, &kind, &whenNano); err != nil {
			return nil, fmt.Errorf("duckindex: scan revision: %w", err)
		}
		records = append(records, ports.Record{
			Path: path,
			Rev:  ports.Revision(rev),
			Kind: kind,
			When: time.Unix(0, whenNano).UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("duckindex: find: %w", err)
	}

	for i := range records {
		fields, err := idx.fieldsFor(ctx, records[i].Path, records[i].Rev)
		if err != nil {
			return nil, err
		}
		records[i].Fields = fields
	}
	return records, nil
}

// fieldsFor returns the field map for one revision of path.
func (idx *Index) fieldsFor(ctx context.Context, path string, rev ports.Revision) (map[string]string, error) {
	rows, err := idx.db.QueryContext(ctx, "SELECT name, value FROM fields WHERE path = ? AND rev = ?", path, string(rev))
	if err != nil {
		return nil, fmt.Errorf("duckindex: fields for %s@%s: %w", path, rev, err)
	}
	defer rows.Close()

	fields := map[string]string{}
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return nil, fmt.Errorf("duckindex: scan field for %s@%s: %w", path, rev, err)
		}
		fields[name] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("duckindex: fields for %s@%s: %w", path, rev, err)
	}
	return fields, nil
}

// Reset deletes every row from both tables without dropping them, so the
// schema survives.
func (idx *Index) Reset(ctx context.Context) error {
	tx, err := idx.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("duckindex: begin reset: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "DELETE FROM fields"); err != nil {
		return fmt.Errorf("duckindex: reset fields: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM revisions"); err != nil {
		return fmt.Errorf("duckindex: reset revisions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("duckindex: commit reset: %w", err)
	}
	return nil
}
