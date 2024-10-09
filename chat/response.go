package chat

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/a-h/ragmark/db"
	"github.com/a-h/ragmark/prompts"
	"github.com/a-h/ragmark/rag"
	ollamaapi "github.com/ollama/ollama/api"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

func NewResponseHandler(log *slog.Logger, r *rag.RAG, oc *ollamaapi.Client, chatModel string) ResponseHandler {
	return ResponseHandler{
		Log:       log,
		RAG:       r,
		ChatModel: chatModel,
		oc:        oc,
	}
}

type ResponseHandlerRequest struct {
	// The message to process.
	Msg string
	// The state of the conversation, in JSON format.
	State string
}

var gm = goldmark.New(goldmark.WithExtensions(extension.Table))

type ResponseHandler struct {
	Log       *slog.Logger
	RAG       *rag.RAG
	ChatModel string
	oc        *ollamaapi.Client
}

func (h ResponseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	prompt := r.URL.Query().Get("prompt")
	noContext := r.URL.Query().Get("no-context") == "true"

	//TODO: Tighten up the CORS policy to not allow all origins.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var chunks []db.Chunk
	if !noContext {
		var err error
		chunks, err = h.RAG.GetContext(r.Context(), prompt)
		if err != nil {
			h.Log.Error("failed to get chunks", slog.Any("error", err))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	req := &ollamaapi.ChatRequest{
		Model: h.ChatModel,
		Messages: []ollamaapi.Message{
			{
				Role:    "user",
				Content: prompts.Chat(chunks, prompt),
			},
		},
	}

	response := new(strings.Builder)

	buf := new(bytes.Buffer)
	fn := func(resp ollamaapi.ChatResponse) (err error) {
		buf.Reset()

		response.WriteString(resp.Message.Content)

		if gm.Convert([]byte(response.String()), buf); err != nil {
			h.Log.Error("failed to convert markdown to HTML", slog.Any("error", err))
			return err
		}
		return writeEvent(w, "message", string(buf.Bytes()))
	}
	if err := h.oc.Chat(r.Context(), req, fn); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Close the response.
	writeEvent(w, "end", "")
}

func writeEvent(w http.ResponseWriter, eventName, data string) (err error) {
	if strings.Contains(eventName, "\n") {
		return fmt.Errorf("event name must not contain a newline")
	}
	if _, err = fmt.Fprintf(w, "event: %s\n", eventName); err != nil {
		return err
	}
	for _, line := range strings.Split(data, "\n") {
		if _, err = fmt.Fprintf(w, "data: %s\n", line); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(w, "\n")
	if flusher, canFlush := w.(http.Flusher); canFlush {
		flusher.Flush()
	}
	return err
}
