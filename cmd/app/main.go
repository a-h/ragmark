package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	ollamaapi "github.com/ollama/ollama/api"

	"github.com/a-h/ragmark/db"
	"github.com/a-h/ragmark/hugowalker"
	"github.com/a-h/ragmark/splitter"
	"github.com/rqlite/gorqlite"

	"github.com/gohugoio/hugo/resources/page"
)

func main() {
	ctx := context.Background()
	if err := run(ctx); err != nil {
		getLogger("error").Error("failed", slog.Any("error", err))
		os.Exit(1)
	}
}

var defaultUsage = `strategy is a tool for managing a Hugo site and syncing it with a search index.

Usage:
  strategy [command]

Commands:
  chat    Chat with the LLM server.
  sync    Sync the Hugo site with the search index.
`

func getLogger(level string) *slog.Logger {
	ll := slog.LevelInfo
	switch level {
	case "debug":
		ll = slog.LevelDebug
	case "info":
		ll = slog.LevelInfo
	case "warn":
		ll = slog.LevelWarn
	case "error":
		ll = slog.LevelError
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: ll,
	}))
}

func run(ctx context.Context) error {
	if len(os.Args) < 2 {
		fmt.Println(defaultUsage)
		return fmt.Errorf("no command specified, use 'chat' or 'sync'")
	}

	switch os.Args[1] {
	case "chat":
		return chat(ctx)
	case "sync":
		return sync(ctx)
	default:
		return fmt.Errorf("unknown command: %s", os.Args[1])
	}
}

func chat(ctx context.Context) (err error) {
	chatFlags := flag.NewFlagSet("chat", flag.ExitOnError)
	model := chatFlags.String("model", "mistral-nemo", "The model to chat with.")
	msg := chatFlags.String("msg", "", "The message to send.")
	nc := chatFlags.Bool("no-context", false, "Set to skip context retrieval and use the base model")
	level := chatFlags.String("level", "warn", "The log level to use, set to info for additional logs")
	if err = chatFlags.Parse(os.Args[2:]); err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}
	if *msg == "" {
		return fmt.Errorf("no message specified")
	}
	log := getLogger(*level)

	databaseURL := db.URL{
		User:     "admin",
		Password: "secret",
		Host:     "localhost",
		Port:     4001,
		Secure:   false,
	}

	log.Info("connecting to database")
	conn, err := gorqlite.Open(databaseURL.DataSourceName())
	if err != nil {
		return fmt.Errorf("failed to open connection: %w", err)
	}
	defer conn.Close()
	queries := db.New(conn)

	log.Info("migrating database schema")
	if err = db.Migrate(databaseURL); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}

	log.Info("creating LLM client")
	ollamaURL, err := url.Parse("http://127.0.0.1:11434/")
	if err != nil {
		return fmt.Errorf("failed to parse LLM URL: %w", err)
	}
	httpClient := &http.Client{}
	oc := ollamaapi.NewClient(ollamaURL, httpClient)

	log.Info("getting context")
	var chunks []db.Chunk
	if !*nc {
		chunks, err = getContext(ctx, log, queries, oc, *msg)
		if err != nil {
			return err
		}
	}

	var sb strings.Builder
	sb.WriteString("Use the following pieces of context to answer the question at the end. If you don't know the answer, just say that you don't know, don't try to make up an answer. Reference the path to relevant context.\n")

	for _, doc := range chunks {
		sb.WriteString(fmt.Sprintf("Context from %s:\n%s\n\n", doc.Path, doc.Text))
	}
	sb.WriteString("Question: ")
	sb.WriteString(*msg)
	sb.WriteString("\nSuccint Answer: ")

	log.Info("starting chat", slog.String("prompt", sb.String()), slog.Int("kb", len(sb.String())/1024))

	req := &ollamaapi.ChatRequest{
		Model: *model,
		Messages: []ollamaapi.Message{
			{
				Role:    "user",
				Content: sb.String(),
			},
		},
	}
	fn := func(resp ollamaapi.ChatResponse) (err error) {
		os.Stdout.WriteString(resp.Message.Content)
		return nil
	}
	return oc.Chat(ctx, req, fn)
}

func getNearestChunks(ctx context.Context, queries *db.Queries, oc *ollamaapi.Client, input string) (chunks []db.ChunkSelectNearestResult, err error) {
	embeddings, err := oc.Embed(ctx, &ollamaapi.EmbedRequest{
		Model: "mistral-nemo",
		Input: input,
	})
	if err != nil {
		return chunks, fmt.Errorf("failed to get message embeddings: %w", err)
	}
	chunks, err = queries.ChunkSelectNearest(ctx, db.ChunkSelectNearestArgs{
		Embedding: embeddings.Embeddings[0],
		Limit:     10,
	})
	if err != nil {
		return chunks, fmt.Errorf("failed to get nearest documents: %w", err)
	}
	return chunks, nil
}

func getChunkContext(ctx context.Context, queries *db.Queries, chunks []db.ChunkSelectNearestResult, n int) (result []db.Chunk, err error) {
	previousChunks := map[string]struct{}{}
	for _, chunk := range chunks {
		chunkRange, err := queries.ChunkSelectRange(ctx, db.ChunkSelectRangeArgs{
			Path:       chunk.Path,
			StartIndex: chunk.Index - n,
			EndIndex:   chunk.Index + n,
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

func getContext(ctx context.Context, log *slog.Logger, queries *db.Queries, oc *ollamaapi.Client, msg string) (chunks []db.Chunk, err error) {
	nearest, err := getNearestChunks(ctx, queries, oc, msg)
	if err != nil {
		return chunks, fmt.Errorf("failed to get message embeddings: %w", err)
	}

	log.Info("found nearest chunks", slog.Int("count", len(nearest)))
	for _, result := range nearest {
		log.Info("result", slog.String("doc", result.Path), slog.Float64("distance", result.Distance), slog.Int("index", result.Index))
	}

	log.Info("getting surrounding context for chunks")
	return getChunkContext(ctx, queries, nearest, 10)
}

func sync(ctx context.Context) (err error) {
	syncFlags := flag.NewFlagSet("sync", flag.ExitOnError)
	level := syncFlags.String("level", "info", "The log level to use, set to info for additional logs")
	if err = syncFlags.Parse(os.Args[2:]); err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}
	log := getLogger(*level)

	databaseURL := db.URL{
		User:     "admin",
		Password: "secret",
		Host:     "localhost",
		Port:     4001,
		Secure:   false,
	}

	log.Info("connecting to database")
	conn, err := gorqlite.Open(databaseURL.DataSourceName())
	if err != nil {
		return fmt.Errorf("failed to open connection: %w", err)
	}
	defer conn.Close()
	queries := db.New(conn)

	log.Info("migrating database schema")
	if err = db.Migrate(databaseURL); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}

	log.Info("creating LLM client")
	ollamaURL, err := url.Parse("http://127.0.0.1:11434/")
	if err != nil {
		return fmt.Errorf("failed to parse LLM URL: %w", err)
	}
	httpClient := &http.Client{}
	oc := ollamaapi.NewClient(ollamaURL, httpClient)

	log.Info("starting process")

	hw, err := hugowalker.New("./site")
	if err != nil {
		return fmt.Errorf("failed to create Hugo walker: %w", err)
	}
	hw.Walk(func(p page.Page) bool {
		if !p.IsPage() {
			return true
		}

		log := log.With(slog.String("path", p.Path()))

		log.Info("processing page")

		log.Info("getting document metadata")
		documentMetadata, err := queries.DocumentUpsert(ctx, db.DocumentUpsertArgs{
			Path: p.Path(),
		})
		if err != nil {
			log.Error("failed to get document metadata", slog.Any("error", err))
			return false
		}

		if p.Lastmod().Before(documentMetadata.LastUpdated) {
			log.Info("document is up to date")
			return true
		}
		log.Info("document is out of date")

		//TODO: Pull out the p.Params() to pull the metadata etc.
		log.Info("upserting document fts index")
		err = queries.DocumentFTSUpsert(ctx, db.DocumentFTSUpsertArgs{
			Path:    p.Path(),
			Title:   p.Title(),
			Text:    p.Plain(ctx),
			Summary: string(p.Summary(ctx)),
		})
		if err != nil {
			log.Error("failed to upsert document fts index", slog.Any("error", err))
			return false
		}

		chunks := splitter.Split(p.Plain(ctx))
		log.Info("processing document chunks", slog.Int("count", len(chunks)))

		var chunkInsertArgs db.ChunkInsertArgs
		chunkInsertArgs.Chunks = make([]db.Chunk, len(chunks))
		log.Info("getting embeddings")
		embeddings, err := oc.Embed(ctx, &ollamaapi.EmbedRequest{
			Model: "mistral-nemo",
			Input: chunks,
		})
		if err != nil {
			log.Error("failed to get chunk embeddings", slog.Any("error", err))
			return false
		}
		for i, chunk := range chunks {
			chunkInsertArgs.Chunks[i] = db.Chunk{
				Path:      p.Path(),
				Index:     i,
				Text:      chunk,
				Embedding: embeddings.Embeddings[i],
			}
		}

		log.Info("deleting existing document chunks")
		err = queries.ChunkDelete(ctx, db.ChunkDeleteArgs{
			Path: p.Path(),
		})
		if err != nil {
			log.Error("failed to delete document index", slog.Any("error", err))
			return false
		}

		log.Info("inserting new document chunks")
		if err = queries.ChunkInsert(ctx, chunkInsertArgs); err != nil {
			log.Error("failed to insert chunks", slog.Any("error", err))
		}

		log.Info("updating last updated time")
		if err = queries.DocumentUpdateLastUpdated(ctx, db.DocumentUpdateLastUpdatedArgs{
			Path:        p.Path(),
			LastUpdated: time.Now(),
		}); err != nil {
			log.Error("failed to update last updated time", slog.Any("error", err))
			return false
		}
		log.Info("inserted document index")

		return true
	})
	log.Info("update complete")
	return nil
}
