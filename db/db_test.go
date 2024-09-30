package db_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/a-h/ragmark/db"
	"github.com/google/go-cmp/cmp"
	"github.com/rqlite/gorqlite"
)

var initOnce sync.Once
var conn *gorqlite.Connection

func initConnection() (err error) {
	initOnce.Do(func() {
		databaseURL := db.URL{
			User:     "admin",
			Password: "secret",
			Host:     "localhost",
			Port:     4001,
			Secure:   false,
		}
		conn, err = gorqlite.Open(databaseURL.DataSourceName())
		if err != nil {
			err = fmt.Errorf("failed to open connection: %w", err)
		}
		if err = db.Migrate(databaseURL); err != nil {
			err = fmt.Errorf("failed to migrate database: %w", err)
		}
	})
	return err
}

func TestTriples(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	if err := initConnection(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	q := db.New(conn)

	t1 := db.Triple{
		Subject:   "subject",
		Predicate: "predicate",
		Object:    "object",
	}
	t2 := db.Triple{
		Subject:   "subject",
		Predicate: "has_used",
		Object:    "object",
	}
	t.Run("Delete can delete existing records", func(t *testing.T) {
		if err := q.TripleDelete(ctx, t1); err != nil {
			t.Fatal(err)
		}
		if err := q.TripleDelete(ctx, t2); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Upsert can insert a new record", func(t *testing.T) {
		err := q.TripleUpsert(ctx, t1)
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("Upsert can overwrite an existing record", func(t *testing.T) {
		err := q.TripleUpsert(ctx, t1)
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("Upsert can insert a new record", func(t *testing.T) {
		err := q.TripleUpsert(ctx, t2)
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("SelectSubject can find existing records", func(t *testing.T) {
		triples, err := q.TripleSelectSubject(ctx, "subject")
		if err != nil {
			t.Fatal(err)
		}
		if len(triples) != 2 {
			t.Fatalf("expected 1 triple, got %d", len(triples))
		}
		if diff := cmp.Diff(triples[0], t1); diff != "" {
			t.Fatalf("unexpected triple: %s", diff)
		}
		if diff := cmp.Diff(triples[1], t2); diff != "" {
			t.Fatalf("unexpected triple: %s", diff)
		}
	})
	t.Run("SelectObject can find existing records", func(t *testing.T) {
		triples, err := q.TripleSelectObject(ctx, "object")
		if err != nil {
			t.Fatal(err)
		}
		if len(triples) != 2 {
			t.Fatalf("expected 1 triple, got %d", len(triples))
		}
		if diff := cmp.Diff(triples[0], t1); diff != "" {
			t.Fatalf("unexpected triple: %s", diff)
		}
		if diff := cmp.Diff(triples[1], t2); diff != "" {
			t.Fatalf("unexpected triple: %s", diff)
		}
	})
	t.Run("Delete can delete existing records", func(t *testing.T) {
		if err := q.TripleDelete(ctx, t1); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("SelectSubject can find existing records", func(t *testing.T) {
		triples, err := q.TripleSelectSubject(ctx, "subject")
		if err != nil {
			t.Fatal(err)
		}
		if len(triples) != 1 {
			t.Fatalf("expected 0 triples, got %d", len(triples))
		}
		if diff := cmp.Diff(triples[0], t2); diff != "" {
			t.Fatalf("unexpected triple: %s", diff)
		}
	})
}
