package site_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/a-h/ragmark/site"
	"github.com/google/go-cmp/cmp"
)

func TestMarkdown(t *testing.T) {
	var indexMD = `---
url: /
title: Home
summary: The home page.
mimeType: text/markdown
lastMod: 2021-01-01T00:00:00Z
---

# Title

Content
`

	dirFS := make(fstest.MapFS)
	dirFS["index.md"] = &fstest.MapFile{
		Data: []byte(indexMD),
	}
	s, err := site.New(site.SiteArgs{
		Dir: dirFS,
		ContentHandlers: []site.DirEntryHandler{
			site.NewMarkdownDirEntryHandler(func(site *site.Site, page site.Metadata, outputHTML string, err error) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					io.WriteString(w, outputHTML)
				})
			}),
			site.NewDirectoryDirEntryHandler(func(s *site.Site, dir site.Metadata, children []site.Metadata) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.NotFound(w, r)
				})
			}),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error processing site: %v", err)
	}

	content, ok := s.GetContent("/")
	if !ok {
		t.Fatalf("content not found, %+v", s.Content())
	}

	t.Run("text can be extracted from content", func(t *testing.T) {
		text, err := content.Text()
		if err != nil {
			t.Fatalf("unexpected error reading text: %v", err)
		}

		expectedMarkdownText := "Title\n\nContent\n\n"
		if diff := cmp.Diff(expectedMarkdownText, text); diff != "" {
			t.Errorf("unexpected text (-want +got):\n%s", diff)
		}
	})
	t.Run("metadata can be extracted from content", func(t *testing.T) {
		expectedMetadata := site.Metadata{
			URL:      "/",
			Title:    "Home",
			Summary:  "The home page.",
			MimeType: "text/markdown",
			LastMod:  time.Date(2021, time.January, 1, 0, 0, 0, 0, time.UTC),
		}
		if diff := cmp.Diff(expectedMetadata, content.Metadata()); diff != "" {
			t.Errorf("unexpected metadata (-want +got):\n%s", diff)
		}
	})
	t.Run("can serve HTTP", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		content.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("unexpected status code: %v", w.Code)
		}
		expectedHTML := "<h1>Title</h1>\n<p>Content</p>\n"

		if diff := cmp.Diff(expectedHTML, w.Body.String()); diff != "" {
			t.Errorf("unexpected HTML (-want +got):\n%s", diff)
		}
	})
	t.Run("cannot serve HTTP for unknown URL", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/unknown", nil)
		s.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("unexpected status code: %v - body %q", w.Code, w.Body.String())
		}
	})
}
