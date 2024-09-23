package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	ollamaapi "github.com/ollama/ollama/api"

	"github.com/a-h/ragmark/db"
	"github.com/a-h/ragmark/site"
	"github.com/a-h/ragmark/splitter"
	"github.com/a-h/ragmark/templates"
	"github.com/a-h/templ"
	"github.com/rqlite/gorqlite"
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
  sync    Populate the search database.
	serve   Serve the website.
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
	case "serve":
		return serve(ctx)

	default:
		return fmt.Errorf("unknown command: %s", os.Args[1])
	}
}

func chat(ctx context.Context) (err error) {
	chatFlags := flag.NewFlagSet("chat", flag.ExitOnError)
	embeddingModel := chatFlags.String("embedding-model", "nomic-embed-text", "The model to chat with.")
	model := chatFlags.String("chat-model", "mistral-nemo", "The model to chat with.")
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
		chunks, err = getContext(ctx, log, queries, oc, *embeddingModel, *msg)
		if err != nil {
			return err
		}
	}

	var sb strings.Builder
	sb.WriteString("Use the following pieces of context to answer the question at the end. If you don't know the answer, just say that you don't know, don't try to make up an answer.\n")

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

func getNearestChunks(ctx context.Context, queries *db.Queries, oc *ollamaapi.Client, model, input string) (chunks []db.ChunkSelectNearestResult, err error) {
	embeddings, err := oc.Embed(ctx, &ollamaapi.EmbedRequest{
		Model: model,
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

func getContext(ctx context.Context, log *slog.Logger, queries *db.Queries, oc *ollamaapi.Client, model, msg string) (chunks []db.Chunk, err error) {
	nearest, err := getNearestChunks(ctx, queries, oc, model, msg)
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
	flags := flag.NewFlagSet("sync", flag.ExitOnError)
	embeddingModel := flags.String("embedding-model", "nomic-embed-text", "The model to use for embeddings.")
	level := flags.String("level", "info", "The log level to use, set to info for additional logs")
	baseURL := flags.String("base-url", "/", "The base URL of the site")
	title := flags.String("title", "ragmark site", "Title of site")
	if err = flags.Parse(os.Args[2:]); err != nil {
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

	site, err := site.New(site.SiteArgs{
		Log:     log,
		Dir:     os.DirFS("./content"),
		BaseURL: *baseURL,
		Title:   *title,
		ContentHandlers: []site.DirEntryHandler{
			dirHandler,
			mdHandler,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create content walker: %w", err)
	}
	for url, content := range site.Content() {
		log := log.With(slog.String("url", url))

		log.Info("processing content")

		log.Info("getting document metadata")
		dbMetadata, err := queries.DocumentUpsert(ctx, db.DocumentUpsertArgs{
			Path: url,
		})
		if err != nil {
			return fmt.Errorf("failed to get document metadata from db: %w", err)
		}

		if content.Metadata().LastMod.Before(dbMetadata.LastUpdated) {
			log.Info("document is up to date")
		}
		log.Info("document is out of date")

		if !strings.HasPrefix(content.Metadata().MimeType, "text/html") {
			log.Info("content is not HTML, skipping")
			continue
		}

		log.Info("upserting document fts index")
		text, err := content.Text()
		if err != nil {
			return fmt.Errorf("failed to get document text: %w", err)
		}
		err = queries.DocumentFTSUpsert(ctx, db.DocumentFTSUpsertArgs{
			Path:    url,
			Title:   content.Metadata().Title,
			Text:    text,
			Summary: content.Metadata().Summary,
		})
		if err != nil {
			return fmt.Errorf("failed to upsert document fts index: %w", err)
		}

		chunks := splitter.Split(text)
		log.Info("processing document chunks", slog.Int("count", len(chunks)))

		var chunkInsertArgs db.ChunkInsertArgs
		chunkInsertArgs.Chunks = make([]db.Chunk, len(chunks))
		log.Info("getting embeddings")
		embeddings, err := oc.Embed(ctx, &ollamaapi.EmbedRequest{
			Model: *embeddingModel,
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

		log.Info("deleting existing document chunks")
		err = queries.ChunkDelete(ctx, db.ChunkDeleteArgs{
			Path: url,
		})
		if err != nil {
			return fmt.Errorf("failed to delete document index: %w", err)
		}

		log.Info("inserting new document chunks")
		if err = queries.ChunkInsert(ctx, chunkInsertArgs); err != nil {
			return fmt.Errorf("failed to insert chunks: %w", err)
		}

		log.Info("updating last updated time")
		if err = queries.DocumentUpdateLastUpdated(ctx, db.DocumentUpdateLastUpdatedArgs{
			Path:        url,
			LastUpdated: time.Now(),
		}); err != nil {
			return fmt.Errorf("failed to update last updated time: %w", err)
		}
		log.Info("inserted document index")
	}
	log.Info("update complete")
	return nil
}

// Handle empty directories.
var dirHandler = site.NewDirectoryDirEntryHandler(func(s *site.Site, dir site.Metadata, children []site.Metadata) http.Handler {
	left := templates.Left(s)
	middle := templates.Directory(dir, children)
	right := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		return nil
	})
	return templ.Handler(templates.Page(left, middle, right))
})

// Handle Markdown files.
var mdHandler = site.NewMarkdownDirEntryHandler(func(s *site.Site, page site.Metadata, outputHTML string, err error) http.Handler {
	if err != nil {
		s.Log.Error("failed to render markdown", slog.String("url", page.URL), slog.Any("error", err))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "failed to render markdown", http.StatusInternalServerError)
		})
	}
	left := templates.Left(s)
	middle := templ.Raw(outputHTML)
	right := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		return nil
	})
	return templ.Handler(templates.Page(left, middle, right))
})

func serve(ctx context.Context) (err error) {
	flags := flag.NewFlagSet("chat", flag.ExitOnError)
	level := flags.String("level", "info", "The log level to use, set to debug for additional logs")
	baseURL := flags.String("base-url", "/", "The base URL of the site")
	title := flags.String("title", "ragmark site", "Title of site")
	if err = flags.Parse(os.Args[2:]); err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}
	log := getLogger(*level)

	s, err := site.New(site.SiteArgs{
		Log:     log,
		Dir:     os.DirFS("./content"),
		BaseURL: *baseURL,
		Title:   *title,
		ContentHandlers: []site.DirEntryHandler{
			dirHandler,
			mdHandler,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to load site: %w", err)
	}

	var contentCount int
	for url, c := range s.Content() {
		log.Info(url, slog.String("metadataURL", c.Metadata().URL))
		contentCount++
	}
	if contentCount == 0 {
		log.Warn("no content to serve")
	}

	log.Info("starting server", slog.String("addr", ":1414"))

	mux := http.NewServeMux()
	mux.Handle("/", s)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	return http.ListenAndServe("localhost:1414", mux)
}
