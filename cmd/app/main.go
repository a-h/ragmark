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
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(ctx, log); err != nil {
		log.Error("failed", slog.Any("error", err))
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

func run(ctx context.Context, log *slog.Logger) error {
	if len(os.Args) < 2 {
		fmt.Println(defaultUsage)
		return fmt.Errorf("no command specified, use 'chat' or 'sync'")
	}

	log.Info("performing DB migrations")
	databaseURL := db.URL{
		User:     "admin",
		Password: "secret",
		Host:     "localhost",
		Port:     4001,
		Secure:   false,
	}
	if err := db.Migrate(databaseURL); err != nil {
		log.Error("migrations failed", slog.Any("error", err))
		os.Exit(1)
	}

	log.Info("connecting to database")
	conn, err := gorqlite.Open(databaseURL.DataSourceName())
	if err != nil {
		log.Error("failed to open connection", slog.Any("error", err))
		os.Exit(1)
	}
	defer conn.Close()
	queries := db.New(conn)

	// Initialize LLM.
	log.Info("creating LLM client")
	ollamaURL, err := url.Parse("http://127.0.0.1:11434/")
	if err != nil {
		return fmt.Errorf("failed to parse LLM URL: %w", err)
	}
	httpClient := &http.Client{}
	oc := ollamaapi.NewClient(ollamaURL, httpClient)

	switch os.Args[1] {
	case "chat":
		return chat(ctx, log, queries, oc)
	case "sync":
		return sync(ctx, log, queries, oc)
	default:
		return fmt.Errorf("unknown command: %s", os.Args[1])
	}
}

func rag(ctx context.Context, queries *db.Queries, oc *ollamaapi.Client, input string) (docs []db.ChunkSelectNearestResult, err error) {
	embeddings, err := oc.Embed(ctx, &ollamaapi.EmbedRequest{
		Model: "mistral-nemo",
		Input: input,
	})
	if err != nil {
		return docs, fmt.Errorf("failed to get message embeddings: %w", err)
	}
	docs, err = queries.ChunkSelectNearest(ctx, db.ChunkSelectNearestArgs{
		Embedding: embeddings.Embeddings[0],
		Limit:     10,
	})
	if err != nil {
		return docs, fmt.Errorf("failed to get nearest documents: %w", err)
	}
	return docs, nil
}

func chat(ctx context.Context, log *slog.Logger, queries *db.Queries, oc *ollamaapi.Client) (err error) {
	syncFlags := flag.NewFlagSet("sync", flag.ExitOnError)
	model := syncFlags.String("model", "mistral-nemo", "The model to chat with.")
	msg := syncFlags.String("msg", "", "The message to send.")
	if err = syncFlags.Parse(os.Args[2:]); err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}
	if *msg == "" {
		return fmt.Errorf("no message specified")
	}

	chunks, err := rag(ctx, queries, oc, *msg)
	if err != nil {
		return fmt.Errorf("failed to get message embeddings: %w", err)
	}

	log.Info("found context", slog.Int("count", len(chunks)))
	if len(chunks) > 0 {
		log.Info("top result", slog.String("doc", chunks[0].Path), slog.String("text", chunks[0].Text), slog.Float64("distance", chunks[0].Distance))
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

func sync(ctx context.Context, log *slog.Logger, queries *db.Queries, oc *ollamaapi.Client) (err error) {
	log.Info("starting up")

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
