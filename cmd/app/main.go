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

	"github.com/rqlite/gorqlite"
	"github.com/a-h/ragmark/db"
	"github.com/a-h/ragmark/hugowalker"

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
		return chat(ctx, queries, oc)
	case "sync":
		return sync(ctx, log, queries, oc)
	default:
		return fmt.Errorf("unknown command: %s", os.Args[1])
	}
}

func rag(ctx context.Context, queries *db.Queries, oc *ollamaapi.Client, input string) (docs []db.DocumentSelectNearestResult, err error) {
	embeddings, err := oc.Embed(ctx, &ollamaapi.EmbedRequest{
		Model: "mistral-nemo",
		Input: input,
	})
	if err != nil {
		return docs, fmt.Errorf("failed to get message embeddings: %w", err)
	}
	docs, err = queries.DocumentsSelectNearest(ctx, embeddings.Embeddings[0], 10)
	if err != nil {
		return docs, fmt.Errorf("failed to get nearest documents: %w", err)
	}
	return docs, nil
}

func chat(ctx context.Context, queries *db.Queries, oc *ollamaapi.Client) (err error) {
	syncFlags := flag.NewFlagSet("sync", flag.ExitOnError)
	model := syncFlags.String("model", "mistral-nemo", "The model to chat with.")
	msg := syncFlags.String("msg", "", "The message to send.")
	if err = syncFlags.Parse(os.Args[2:]); err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}
	if *msg == "" {
		return fmt.Errorf("no message specified")
	}

	docs, err := rag(ctx, queries, oc, *msg)
	if err != nil {
		return fmt.Errorf("failed to get message embeddings: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("Use the following pieces of context to answer the question at the end. If you don't know the answer, just say that you don't know, don't try to make up an answer.\n")

	for _, doc := range docs {
		sb.WriteString(fmt.Sprintf("## %s\n", doc.Path))
		sb.WriteString(fmt.Sprintf("## Context\n%s\n", doc.Text))
	}
	sb.WriteString("Question: ")
	sb.WriteString(*msg)
	sb.WriteString("\nHelpful Answer: ")

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

	// Example of printing out the inserted value.
	//doc, _, err := queries.DocumentsSelectOne(ctx, "/docs/example/table-of-contents/with-toc")
	//if err != nil {
	//log.Error("failed to select document", slog.Any("error", err))
	//return err
	//}

	hw, err := hugowalker.New("./site")
	if err != nil {
		return fmt.Errorf("failed to create Hugo walker: %w", err)
	}
	hw.Walk(func(p page.Page) bool {
		if !p.IsPage() {
			return true
		}
		log.Info("processing page", slog.Any("path", p.Path()))

		log.Info("getting document metadata", slog.Any("path", p.Path()))
		documentMetadata, err := queries.DocumentsUpsert(ctx, db.DocumentsUpsertArgs{
			Path: p.Path(),
		})
		if err != nil {
			log.Error("failed to get document metadata", slog.Any("error", err), slog.String("errorType", fmt.Sprintf("%T", err)))
			return false
		}

		if p.Lastmod().Before(documentMetadata.LastUpdated) {
			log.Info("document is up to date", slog.Any("path", p.Path()))
			return true
		}

		log.Info("document is out of date", slog.Any("path", p.Path()))

		log.Info("getting embeddings", slog.Any("path", p.Path()))
		embeddings, err := oc.Embed(ctx, &ollamaapi.EmbedRequest{
			Model: "mistral-nemo",
			Input: p.Plain(context.Background()),
		})
		if err != nil {
			log.Error("failed to embed document", slog.Any("error", err))
			return false
		}

		//TODO: Pull out the p.Params() to pull the metadata etc.
		log.Info("deleting existing document index", slog.Int64("id", documentMetadata.ID), slog.String("path", p.Path()))
		err = queries.DocumentsIndexDelete(ctx, db.DocumentsIndexDeleteArgs{
			DocumentID: documentMetadata.ID,
		})
		if err != nil {
			log.Error("failed to delete document index", slog.Int64("id", documentMetadata.ID), slog.String("path", p.Path()), slog.Any("error", err))
			return false
		}
		log.Info("creating new document index", slog.Int64("id", documentMetadata.ID), slog.String("path", p.Path()))
		err = queries.DocumentsIndexInsert(ctx, db.DocumentsIndexInsertArgs{
			DocumentID: documentMetadata.ID,
			Title:      p.Title(),
			Text:       p.Plain(ctx),
			Summary:    string(p.Summary(ctx)),
			Embedding:  embeddings.Embeddings[0], // For a single input, there is a single embedding.
		})
		if err != nil {
			log.Error("failed to insert document index", slog.Int64("id", documentMetadata.ID), slog.String("path", p.Path()), slog.Any("error", err))
			return false
		}
		log.Info("updating last updated time", slog.Int64("id", documentMetadata.ID), slog.String("path", p.Path()))
		if err = queries.DocumentsUpdateLastUpdated(ctx, db.DocumentsUpdateLastUpdatedArgs{
			ID:          documentMetadata.ID,
			LastUpdated: time.Now(),
		}); err != nil {
			log.Error("failed to update last updated time", slog.Any("id", documentMetadata.ID), slog.Any("path", p.Path()), slog.Any("error", err))
			return false
		}
		log.Info("inserted document index", slog.Any("id", documentMetadata.ID), slog.Any("path", p.Path()))

		return true
	})
	log.Info("update complete")
	return nil
}
