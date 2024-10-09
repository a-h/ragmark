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

	ollamaapi "github.com/ollama/ollama/api"

	"github.com/a-h/ragmark/chat"
	"github.com/a-h/ragmark/db"
	"github.com/a-h/ragmark/indexer"
	"github.com/a-h/ragmark/prompts"
	"github.com/a-h/ragmark/rag"
	"github.com/a-h/ragmark/site"
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
  index   Populate the search database.
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
		return chatCmd(ctx)
	case "index":
		return indexCmd(ctx)
	case "serve":
		return serve(ctx)

	default:
		return fmt.Errorf("unknown command: %s", os.Args[1])
	}
}

func chatCmd(ctx context.Context) (err error) {
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
	r := rag.New(log, queries, oc, *embeddingModel)
	var chunks []db.Chunk
	if !*nc {
		chunks, err = r.GetContext(ctx, *msg)
		if err != nil {
			return err
		}
	}

	prompt := prompts.Chat(chunks, *msg)
	log.Info("starting chat", slog.String("prompt", prompt), slog.Int("kb", len(prompt)/1024))

	req := &ollamaapi.ChatRequest{
		Model: *model,
		Messages: []ollamaapi.Message{
			{
				Role:    "user",
				Content: prompt,
			},
		},
	}
	fn := func(resp ollamaapi.ChatResponse) (err error) {
		os.Stdout.WriteString(resp.Message.Content)
		return nil
	}
	return oc.Chat(ctx, req, fn)
}

func indexCmd(ctx context.Context) (err error) {
	flags := flag.NewFlagSet("index", flag.ExitOnError)
	embeddingModel := flags.String("embedding-model", "nomic-embed-text", "The model to use for embeddings.")
	chatModel := flags.String("chat-model", "mistral-nemo", "The model to chat with.")
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

	log.Info("creating site walker")
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

	idx := indexer.New(log, queries, oc, *embeddingModel, *chatModel)
	return idx.Index(ctx, site)
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
var mdHandler = site.NewMarkdownDirEntryHandler(func(s *site.Site, page site.Metadata, toc []site.MenuItem, outputHTML string, err error) http.Handler {
	if err != nil {
		s.Log.Error("failed to render markdown", slog.String("url", page.URL), slog.Any("error", err))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "failed to render markdown", http.StatusInternalServerError)
		})
	}
	left := templates.Left(s)
	middle := templ.Raw(outputHTML)
	right := templates.Right(toc)
	return templ.Handler(templates.Page(left, middle, right))
})

func serve(ctx context.Context) (err error) {
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	embeddingModel := flags.String("embedding-model", "nomic-embed-text", "The model to chat with.")
	chatModel := flags.String("chat-model", "mistral-nemo", "The model to chat with.")
	level := flags.String("level", "info", "The log level to use, set to debug for additional logs")
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

	mux.Handle("/chat", chat.NewFormHandler(s))
	r := rag.New(log, queries, oc, *embeddingModel)
	ch := chat.NewResponseHandler(log, r, oc, *chatModel)
	mux.Handle("/chat/response", ch)

	return http.ListenAndServe("localhost:1414", mux)
}
