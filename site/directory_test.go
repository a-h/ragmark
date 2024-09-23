package site_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/a-h/ragmark/site"
	"github.com/google/go-cmp/cmp"
)

func TestDirectory(t *testing.T) {
	var pageMD = `---
url: /sub/page
title: Home
summary: The home page.
mimeType: text/markdown
lastMod: 2021-01-01T00:00:00Z
---

# Title

Content
`
	dirFS := make(fstest.MapFS)
	dirFS["sub/page.md"] = &fstest.MapFile{
		Data: []byte(pageMD),
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
					io.WriteString(w, fmt.Sprintf("<h1>%s</h1>\n", dir.URL))
					for _, c := range children {
						io.WriteString(w, fmt.Sprintf("<p>%s</p>\n", c.URL))
					}
				})
			}),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error processing site: %v", err)
	}

	t.Run("can serve directory listing", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/sub", nil)
		s.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("unexpected status code: %v", w.Code)
		}
		expectedHTML := `<h1>/sub</h1>
<p>/sub/page</p>
`

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
