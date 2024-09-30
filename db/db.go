package db

import (
	"context"
	"encoding/json"
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

type DocumentUpsertArgs struct {
	Path string
}

type DocumentUpsertResult struct {
	Path        string
	LastUpdated time.Time
}

// DocumentUpsert upserts a document. If the document already exists the record will be
// returned.
// If the document does not exist, it will be inserted, and the updated flag will be set to true.
func (q *Queries) DocumentUpsert(ctx context.Context, args DocumentUpsertArgs) (doc DocumentUpsertResult, err error) {
	results, err := q.conn.QueryOneParameterizedContext(ctx, gorqlite.ParameterizedStatement{
		Query:     `select path, last_updated from document where path = ?`,
		Arguments: []any{args.Path},
	})
	if err != nil {
		return doc, fmt.Errorf("failed to select document rowid: %w", err)
	}
	var hasResult bool
	for results.Next() {
		err := results.Scan(&doc.Path, &doc.LastUpdated)
		if err != nil {
			return doc, err
		}
		hasResult = true
	}
	if hasResult {
		return doc, nil
	}
	_, err = q.conn.WriteOneParameterizedContext(ctx, gorqlite.ParameterizedStatement{
		Query:     `insert or ignore into document (path, last_updated) values (?, ?)`,
		Arguments: []any{args.Path, time.Time{}},
	})
	if err != nil {
		return doc, fmt.Errorf("failed to upsert document: %w", err)
	}
	doc.Path = args.Path
	doc.LastUpdated = time.Time{}
	return doc, nil
}

type DocumentUpdateLastUpdatedArgs struct {
	Path        string
	LastUpdated time.Time
}

func (q *Queries) DocumentUpdateLastUpdated(ctx context.Context, args DocumentUpdateLastUpdatedArgs) (err error) {
	_, err = q.conn.WriteOneParameterizedContext(ctx, gorqlite.ParameterizedStatement{
		Query:     `update document set last_updated = ? where path = ?`,
		Arguments: []any{args.LastUpdated, args.Path},
	})
	if err != nil {
		return fmt.Errorf("failed to upsert document last_updated: %w", err)
	}
	return nil
}

type DocumentFTSUpsertArgs struct {
	Path    string
	Title   string
	Text    string
	Summary string
}

func (q *Queries) DocumentFTSUpsert(ctx context.Context, args DocumentFTSUpsertArgs) (err error) {
	_, err = q.conn.WriteOneParameterizedContext(ctx, gorqlite.ParameterizedStatement{
		Query:     `insert or replace into document_fts (path, title, text, summary) values (?, ?, ?, ?)`,
		Arguments: []any{args.Path, args.Title, args.Text, args.Summary},
	})
	if err != nil {
		return fmt.Errorf("failed to upsert document fts: %w", err)
	}
	return nil
}

type ChunkDeleteArgs struct {
	Path string
}

func (q *Queries) ChunkDelete(ctx context.Context, args ChunkDeleteArgs) (err error) {
	statements := []gorqlite.ParameterizedStatement{
		{
			Query:     `delete from chunk_embedding where rowid in (select rowid from chunk where path = ?)`,
			Arguments: []any{args.Path},
		},
		{
			Query:     `delete from chunk where path = ?`,
			Arguments: []any{args.Path},
		},
	}
	if _, err = q.conn.WriteParameterizedContext(ctx, statements); err != nil {
		return err
	}
	return nil
}

type Chunk struct {
	Path      string
	Index     int
	Text      string
	Embedding []float32
}

type ChunkInsertArgs struct {
	Chunks []Chunk
}

func (q *Queries) ChunkInsert(ctx context.Context, args ChunkInsertArgs) (err error) {
	statements := make([]gorqlite.ParameterizedStatement, len(args.Chunks)*2)
	var chunkIndex = 0
	for _, chunk := range args.Chunks {
		embeddingJSON, err := json.Marshal(chunk.Embedding)
		if err != nil {
			return fmt.Errorf("failed to marshal embedding: %w", err)
		}
		statements[chunkIndex] = gorqlite.ParameterizedStatement{
			Query:     `insert into chunk (path, idx, text) values (?, ?, ?)`,
			Arguments: []any{chunk.Path, chunk.Index, chunk.Text},
		}
		chunkIndex++
		statements[chunkIndex] = gorqlite.ParameterizedStatement{
			Query:     `insert into chunk_embedding (rowid, embedding) values (last_insert_rowid(), ?)`,
			Arguments: []any{string(embeddingJSON)},
		}
		chunkIndex++
	}
	if _, err = q.conn.WriteParameterizedContext(ctx, statements); err != nil {
		return err
	}
	return nil
}

type ChunkSelectArgs struct {
	Path string
}

func (q *Queries) ChunkSelect(ctx context.Context, args ChunkSelectArgs) (chunks []Chunk, err error) {
	query := `select
							c.idx, c.text, vec_to_json(ce.embedding)
						from
							chunk c
						inner join
							chunk_embedding ce on c.rowid = ce.rowid
						where
							c.path = ?
						order by
							c.idx;`
	result, err := q.conn.QueryOneParameterizedContext(ctx, gorqlite.ParameterizedStatement{
		Query:     query,
		Arguments: []any{args.Path},
	})
	if err != nil {
		return chunks, err
	}
	for result.Next() {
		chunk := Chunk{Path: args.Path}
		var embeddingJSON string
		if err = result.Scan(&chunk.Index, &chunk.Text, &embeddingJSON); err != nil {
			return chunks, err
		}
		if err = json.Unmarshal([]byte(embeddingJSON), &chunk.Embedding); err != nil {
			return chunks, fmt.Errorf("failed to unmarshal embedding: %w", err)
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

type ChunkSelectRangeArgs struct {
	Path       string
	StartIndex int
	EndIndex   int
}

func (q *Queries) ChunkSelectRange(ctx context.Context, args ChunkSelectRangeArgs) (chunks []Chunk, err error) {
	query := `select
							c.idx, c.text, vec_to_json(ce.embedding)
						from
							chunk c
						inner join
							chunk_embedding ce on c.rowid = ce.rowid
						where
							c.path = ? and c.idx >= ? and c.idx <= ?
						order by
							c.idx;`
	result, err := q.conn.QueryOneParameterizedContext(ctx, gorqlite.ParameterizedStatement{
		Query:     query,
		Arguments: []any{args.Path, args.StartIndex, args.EndIndex},
	})
	if err != nil {
		return chunks, err
	}
	for result.Next() {
		chunk := Chunk{Path: args.Path}
		var embeddingJSON string
		if err = result.Scan(&chunk.Index, &chunk.Text, &embeddingJSON); err != nil {
			return chunks, err
		}
		if err = json.Unmarshal([]byte(embeddingJSON), &chunk.Embedding); err != nil {
			return chunks, fmt.Errorf("failed to unmarshal embedding: %w", err)
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

type ChunkSelectNearestArgs struct {
	Embedding []float32
	Limit     int
}

type ChunkSelectNearestResult struct {
	Chunk
	Distance float64
}

func (q *Queries) ChunkSelectNearest(ctx context.Context, args ChunkSelectNearestArgs) (chunks []ChunkSelectNearestResult, err error) {
	embeddingInputJSON, err := json.Marshal(args.Embedding)
	if err != nil {
		return chunks, fmt.Errorf("failed to marshal embedding: %w", err)
	}
	stmt := gorqlite.ParameterizedStatement{
		Query: `with vec_results as (
							select
								rowid, embedding, distance
							from
								chunk_embedding
							where
								embedding match ?
							order by distance asc
							limit ?
						)
						select
							c.path, c.idx, c.text, vec_to_json(vr.embedding), vr.distance
						from
							chunk c
						inner join
							vec_results vr on c.rowid = vr.rowid
						order by vr.distance;`,
		Arguments: []any{string(embeddingInputJSON), args.Limit},
	}
	result, err := q.conn.QueryOneParameterizedContext(ctx, stmt)
	if err != nil {
		if result.Err != nil {
			return chunks, result.Err
		}
		return chunks, err
	}
	for result.Next() {
		var chunk ChunkSelectNearestResult
		var embeddingJSON string
		if err = result.Scan(&chunk.Path, &chunk.Index, &chunk.Text, &embeddingJSON, &chunk.Distance); err != nil {
			return chunks, err
		}
		if err = json.Unmarshal([]byte(embeddingJSON), &chunk.Embedding); err != nil {
			return chunks, fmt.Errorf("failed to unmarshal embedding: %w", err)
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

type Triple struct {
	Subject   string `json:"s"`
	Predicate string `json:"p"`
	Object    string `json:"o"`
}

func (q *Queries) TripleUpsert(ctx context.Context, triple Triple) (err error) {
	tripleJSON, err := json.Marshal(triple)
	if err != nil {
		return fmt.Errorf("failed to marshal triple: %w", err)
	}
	_, err = q.conn.WriteOneParameterizedContext(ctx, gorqlite.ParameterizedStatement{
		Query:     `insert or replace into triple (triple) values (?)`,
		Arguments: []any{string(tripleJSON)},
	})
	if err != nil {
		return fmt.Errorf("failed to upsert triple: %w", err)
	}
	return nil
}

func (q *Queries) TripleDelete(ctx context.Context, triple Triple) (err error) {
	_, err = q.conn.WriteOneParameterizedContext(ctx, gorqlite.ParameterizedStatement{
		Query:     `delete from triple where subject = ? and predicate = ? and object = ?`,
		Arguments: []any{triple.Subject, triple.Predicate, triple.Object},
	})
	if err != nil {
		return fmt.Errorf("failed to delete triple: %w", err)
	}
	return nil
}

func (q *Queries) TripleSelectSubject(ctx context.Context, subject string) (triples []Triple, err error) {
	result, err := q.conn.QueryOneParameterizedContext(ctx, gorqlite.ParameterizedStatement{
		Query:     `select triple from triple where subject = ?`,
		Arguments: []any{subject},
	})
	if err != nil {
		return triples, err
	}
	for result.Next() {
		var tripleJSON string
		if err = result.Scan(&tripleJSON); err != nil {
			return triples, err
		}
		var triple Triple
		if err = json.Unmarshal([]byte(tripleJSON), &triple); err != nil {
			return triples, fmt.Errorf("failed to unmarshal triple: %w", err)
		}
		triples = append(triples, triple)
	}
	return triples, nil
}

func (q *Queries) TripleSelectObject(ctx context.Context, object string) (triples []Triple, err error) {
	result, err := q.conn.QueryOneParameterizedContext(ctx, gorqlite.ParameterizedStatement{
		Query:     `select triple from triple where object = ?`,
		Arguments: []any{object},
	})
	if err != nil {
		return triples, err
	}
	for result.Next() {
		var triple Triple
		if err = result.Scan(&triple); err != nil {
			return triples, err
		}
		triples = append(triples, triple)
	}
	return triples, nil
}
