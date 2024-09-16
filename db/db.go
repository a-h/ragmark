package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rqlite/gorqlite"
)

func New(conn *gorqlite.Connection) *Queries {
	return &Queries{
		conn: conn,
		now:  time.Now,
	}
}

type Queries struct {
	conn *gorqlite.Connection
	now  func() time.Time
}

type DocumentsUpsertArgs struct {
	Path string `db:"path"`
}

type DocumentsUpsertResult struct {
	ID          int64     `db:"rowid"`
	Path        string    `db:"path"`
	LastUpdated time.Time `db:"last_updated"`
}

// DocumentsUpsert upserts a document. If the document already exists the record will be
// returned.
// If the document does not exist, it will be inserted, and the updated flag will be set to true.
func (q *Queries) DocumentsUpsert(ctx context.Context, args DocumentsUpsertArgs) (doc DocumentsUpsertResult, err error) {
	results, err := q.conn.QueryOneParameterizedContext(ctx, gorqlite.ParameterizedStatement{
		Query:     `select rowid, path, last_updated from documents where path = ?`,
		Arguments: []any{args.Path},
	})
	if err != nil {
		return doc, fmt.Errorf("failed to select document rowid: %w", err)
	}
	var hasResult bool
	for results.Next() {
		err := results.Scan(&doc.ID, &doc.Path, &doc.LastUpdated)
		if err != nil {
			return doc, err
		}
		hasResult = true
	}
	if hasResult {
		return doc, nil
	}
	result, err := q.conn.WriteOneParameterizedContext(ctx, gorqlite.ParameterizedStatement{
		Query:     `insert or ignore into documents (path, last_updated) values (?, ?)`,
		Arguments: []any{args.Path, time.Time{}},
	})
	if err != nil {
		return doc, fmt.Errorf("failed to upsert document rowid: %w", err)
	}
	doc.ID = result.LastInsertID
	doc.Path = args.Path
	doc.LastUpdated = time.Time{}
	return doc, nil
}

type DocumentsUpdateLastUpdatedArgs struct {
	ID          int64     `db:"rowid"`
	LastUpdated time.Time `db:"last_updated"`
}

func (q *Queries) DocumentsUpdateLastUpdated(ctx context.Context, args DocumentsUpdateLastUpdatedArgs) (err error) {
	_, err = q.conn.WriteOneParameterizedContext(ctx, gorqlite.ParameterizedStatement{
		Query:     `update documents set last_updated = ? where rowid = ?`,
		Arguments: []any{args.LastUpdated, args.ID},
	})
	if err != nil {
		return fmt.Errorf("failed to upsert document last_updated: %w", err)
	}
	return nil
}

type DocumentsIndexDeleteArgs struct {
	DocumentID int64 `db:"document_id"`
}

func (q *Queries) DocumentsIndexDelete(ctx context.Context, args DocumentsIndexDeleteArgs) (err error) {
	statements := []gorqlite.ParameterizedStatement{
		{
			Query:     `delete from documents_fts where document_id = ?`,
			Arguments: []any{args.DocumentID},
		},
		{
			Query:     `delete from documents_embeddings where rowid = ?`,
			Arguments: []any{args.DocumentID},
		},
	}
	results, err := q.conn.WriteParameterizedContext(ctx, statements)
	if err != nil {
		var errs []error
		for _, result := range results {
			errs = append(errs, result.Err)
		}
		return errors.Join(errs...)
	}
	return nil
}

type DocumentsIndexInsertArgs struct {
	DocumentID int64     `db:"document_id"`
	Title      string    `db:"title"`
	Text       string    `db:"text"`
	Summary    string    `db:"summary"`
	Embedding  []float32 `db:"embedding"`
}

func (q *Queries) DocumentsIndexInsert(ctx context.Context, args DocumentsIndexInsertArgs) (err error) {
	embeddingJSON, err := json.Marshal(args.Embedding)
	if err != nil {
		return fmt.Errorf("failed to marshal embedding: %w", err)
	}
	statements := []gorqlite.ParameterizedStatement{
		{
			Query: `insert into 
								documents_fts (document_id, title, text, summary)
							values (?, ?, ?, ?)`,
			Arguments: []any{args.DocumentID, args.Title, args.Text, args.Summary},
		},
		{
			Query:     "insert into documents_embeddings (rowid, embedding) values (?, ?)",
			Arguments: []any{args.DocumentID, string(embeddingJSON)},
		},
	}
	results, err := q.conn.WriteParameterizedContext(ctx, statements)
	if err != nil {
		var errs []error
		for _, result := range results {
			errs = append(errs, result.Err)
		}
		return errors.Join(errs...)
	}
	return nil
}

type Document struct {
	ID          int64     `db:"rowid"`
	Path        string    `db:"path"`
	LastUpdated time.Time `db:"last_updated"`
	Title       string    `db:"title"`
	Text        string    `db:"text"`
	Summary     string    `db:"summary"`
	Embedding   []float32 `db:"embedding"`
}

func (q *Queries) DocumentsSelectOne(ctx context.Context, path string) (doc Document, ok bool, err error) {
	query := gorqlite.ParameterizedStatement{
		Query: `select
							d.rowid, d.path, d.last_updated,
							ds.title, ds.text, ds.summary,
							vec_to_json(de.embedding) as embedding
						from
							documents d
						inner join
							documents_fts ds on d.rowid = ds.document_id
						inner join
						  documents_embeddings de on d.rowid = de.rowid
						where d.path = ?`,
		Arguments: []any{path},
	}
	result, err := q.conn.QueryOneParameterized(query)
	if err != nil {
		return doc, ok, err
	}
	for result.Next() {
		var embeddingJSON string
		if err = result.Scan(&doc.ID, &doc.Path, &doc.LastUpdated, &doc.Title, &doc.Text, &doc.Summary, &embeddingJSON); err != nil {
			return doc, ok, err
		}
		if err = json.Unmarshal([]byte(embeddingJSON), &doc.Embedding); err != nil {
			return doc, ok, fmt.Errorf("failed to unmarshal embedding: %w", err)
		}
		ok = true
	}
	return doc, ok, nil
}

func (q *Queries) DocumentsSelectMany(ctx context.Context) (docs []Document, err error) {
	query := `select
							d.rowid, d.path, d.last_updated,
							ds.title, ds.text, ds.summary,
							vec_to_json(de.embedding) as embedding
						from
							documents d
						inner join
							documents_fts ds on d.rowid = ds.document_id;
						inner join
							documents_embeddings de on d.rowid = de.rowid;`
	result, err := q.conn.QueryOneContext(ctx, query)
	if err != nil {
		return docs, err
	}
	for result.Next() {
		var doc Document
		if err = result.Scan(&doc.ID, &doc.Path, &doc.LastUpdated, &doc.Title, &doc.Text, &doc.Summary); err != nil {
			return docs, err
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

type DocumentSelectNearestResult struct {
	Document
	Distance float64 `db:"distance"`
}

func (q *Queries) DocumentsSelectNearest(ctx context.Context, embedding []float32, limit int) (docs []DocumentSelectNearestResult, err error) {
	embeddingInputJSON, err := json.Marshal(embedding)
	if err != nil {
		return docs, fmt.Errorf("failed to marshal embedding: %w", err)
	}
	stmt := gorqlite.ParameterizedStatement{
		Query: `with vec_results as (
							select
								rowid, embedding, distance
							from
								documents_embeddings
							where
								embedding match ? order by distance limit ?
						)
						select
							d.rowid, d.path, d.last_updated,
							ds.title, ds.text, ds.summary,
							vr.distance
						from vec_results vr
							inner join
								documents d on vr.rowid = d.rowid
							inner join
								documents_fts ds on d.rowid = ds.document_id;`,
		Arguments: []any{string(embeddingInputJSON), limit},
	}
	result, err := q.conn.QueryOneParameterizedContext(ctx, stmt)
	if err != nil {
		return docs, err
	}
	for result.Next() {
		var doc DocumentSelectNearestResult
		if err = result.Scan(&doc.ID, &doc.Path, &doc.LastUpdated, &doc.Title, &doc.Text, &doc.Summary, &doc.Distance); err != nil {
			return docs, err
		}
		docs = append(docs, doc)
	}
	return docs, nil
}
