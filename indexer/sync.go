package indexer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/a-h/ragmark/db"
	"github.com/a-h/ragmark/site"
	"github.com/a-h/ragmark/splitter"
	ollamaapi "github.com/ollama/ollama/api"
)

func New(log *slog.Logger, queries *db.Queries, oc *ollamaapi.Client, model string) *Indexer {
	return &Indexer{
		Log:     log,
		Model:   model,
		queries: queries,
		oc:      oc,
	}
}

type Indexer struct {
	Log *slog.Logger
	// Model to use for embeddings.
	Model   string
	queries *db.Queries
	oc      *ollamaapi.Client
}

func (indexer Indexer) Index(ctx context.Context, site *site.Site) (err error) {
	indexer.Log.Info("starting process")
	for url, content := range site.Content() {
		log := indexer.Log.With(slog.String("url", url))

		log.Info("processing content")

		log.Info("getting document metadata")
		dbMetadata, err := indexer.queries.DocumentUpsert(ctx, db.DocumentUpsertArgs{
			Path: url,
		})
		if err != nil {
			return fmt.Errorf("failed to get document metadata from db: %w", err)
		}

		if content.Metadata().LastMod.Before(dbMetadata.LastUpdated) {
			indexer.Log.Info("document is up to date")
		}
		indexer.Log.Info("document is out of date")

		if !strings.HasPrefix(content.Metadata().MimeType, "text/html") {
			indexer.Log.Info("content is not HTML, skipping")
			continue
		}

		indexer.Log.Info("upserting document fts index")
		text, err := content.Text()
		if err != nil {
			return fmt.Errorf("failed to get document text: %w", err)
		}
		err = indexer.queries.DocumentFTSUpsert(ctx, db.DocumentFTSUpsertArgs{
			Path:    url,
			Title:   content.Metadata().Title,
			Text:    text,
			Summary: content.Metadata().Summary,
		})
		if err != nil {
			return fmt.Errorf("failed to upsert document fts index: %w", err)
		}

		chunks := splitter.Split(text)
		indexer.Log.Info("processing document chunks", slog.Int("count", len(chunks)))

		var chunkInsertArgs db.ChunkInsertArgs
		chunkInsertArgs.Chunks = make([]db.Chunk, len(chunks))
		indexer.Log.Info("getting embeddings")
		embeddings, err := indexer.oc.Embed(ctx, &ollamaapi.EmbedRequest{
			Model: indexer.Model,
			Input: chunks,
		})
		if err != nil {
			return fmt.Errorf("failed to get chunk embeddings: %w", err)
		}
		for i, chunk := range chunks {
			chunkInsertArgs.Chunks[i] = db.Chunk{
				Path:      url,
				Index:     i,
				Text:      chunk,
				Embedding: embeddings.Embeddings[i],
			}
		}

		indexer.Log.Info("deleting existing document chunks")
		err = indexer.queries.ChunkDelete(ctx, db.ChunkDeleteArgs{
			Path: url,
		})
		if err != nil {
			return fmt.Errorf("failed to delete document index: %w", err)
		}

		indexer.Log.Info("inserting new document chunks")
		if err = indexer.queries.ChunkInsert(ctx, chunkInsertArgs); err != nil {
			return fmt.Errorf("failed to insert chunks: %w", err)
		}

		indexer.Log.Info("updating last updated time")
		if err = indexer.queries.DocumentUpdateLastUpdated(ctx, db.DocumentUpdateLastUpdatedArgs{
			Path:        url,
			LastUpdated: time.Now(),
		}); err != nil {
			return fmt.Errorf("failed to update last updated time: %w", err)
		}
		indexer.Log.Info("inserted document index")
	}
	indexer.Log.Info("update complete")
	return nil
}
