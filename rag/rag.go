package rag

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/a-h/ragmark/db"
	ollamaapi "github.com/ollama/ollama/api"
)

func New(log *slog.Logger, queries *db.Queries, oc *ollamaapi.Client, model string) *RAG {
	return &RAG{
		Log:           log,
		Model:         model,
		ContextWindow: 10,
		queries:       queries,
		oc:            oc,
	}
}

type RAG struct {
	Log *slog.Logger
	// Model to use for embeddings.
	Model string
	// Number of surrounding chunks to return.
	ContextWindow int
	queries       *db.Queries
	oc            *ollamaapi.Client
}

func (r *RAG) GetContext(ctx context.Context, msg string) (chunks []db.Chunk, err error) {
	nearest, err := r.getNearestChunks(ctx, msg)
	if err != nil {
		return chunks, fmt.Errorf("failed to get message embeddings: %w", err)
	}

	r.Log.Info("found nearest chunks", slog.Int("count", len(nearest)))
	for _, result := range nearest {
		r.Log.Info("result", slog.String("doc", result.Path), slog.Float64("distance", result.Distance), slog.Int("index", result.Index))
	}

	r.Log.Info("getting surrounding context for chunks")
	return r.getChunkContext(ctx, nearest)
}

func (r *RAG) getNearestChunks(ctx context.Context, input string) (chunks []db.ChunkSelectNearestResult, err error) {
	if len(input) == 0 {
		return chunks, fmt.Errorf("input is empty")
	}
	embeddings, err := r.oc.Embed(ctx, &ollamaapi.EmbedRequest{
		Model: r.Model,
		Input: input,
	})
	if err != nil {
		return chunks, fmt.Errorf("failed to get message embeddings: %w", err)
	}
	chunks, err = r.queries.ChunkSelectNearest(ctx, db.ChunkSelectNearestArgs{
		Embedding: embeddings.Embeddings[0],
		Limit:     10,
	})
	if err != nil {
		return chunks, fmt.Errorf("failed to get nearest documents: %w", err)
	}
	return chunks, nil
}

func (r *RAG) getChunkContext(ctx context.Context, chunks []db.ChunkSelectNearestResult) (result []db.Chunk, err error) {
	previousChunks := map[string]struct{}{}
	for _, chunk := range chunks {
		chunkRange, err := r.queries.ChunkSelectRange(ctx, db.ChunkSelectRangeArgs{
			Path:       chunk.Path,
			StartIndex: chunk.Index - r.ContextWindow,
			EndIndex:   chunk.Index + r.ContextWindow,
		})
		if err != nil {
			return result, fmt.Errorf("failed to select chunk range: %w", err)
		}
		for _, chunkInRange := range chunkRange {
			cacheKey := fmt.Sprintf("%s_%d", chunkInRange.Path, chunkInRange.Index)
			if _, previouslyAdded := previousChunks[cacheKey]; previouslyAdded {
				continue
			}
			result = append(result, chunkInRange)
			previousChunks[cacheKey] = struct{}{}
		}
	}
	return result, nil
}
