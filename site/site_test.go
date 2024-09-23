package site_test

import (
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/a-h/ragmark/site"
	"github.com/google/go-cmp/cmp"
)

func TestMenu(t *testing.T) {
	dirFS := make(fstest.MapFS)
	dirFS["index.md"] = &fstest.MapFile{
		Data: []byte("/"),
	}
	dirFS["a/index.md"] = &fstest.MapFile{
		Data: []byte("/a"),
	}
	dirFS["a/1/index.md"] = &fstest.MapFile{
		Data: []byte("/a/1"),
	}
	dirFS["a/1/aa.md"] = &fstest.MapFile{
		Data: []byte("/a/1/aa"),
	}
	dirFS["a/2/index.md"] = &fstest.MapFile{
		Data: []byte("/a/2"),
	}
	dirFS["b/index.md"] = &fstest.MapFile{
		Data: []byte("/b"),
	}
	dirFS["c/index.md"] = &fstest.MapFile{
		Data: []byte("/c"),
	}
	dirFS["d/3/4/index.md"] = &fstest.MapFile{
		Data: []byte("/d/3/4"),
	}
	s, err := site.New(site.SiteArgs{
		Dir: dirFS,
		ContentHandlers: []site.DirEntryHandler{
			site.NewMarkdownDirEntryHandler(func(site *site.Site, page site.Metadata, toc []site.MenuItem, outputHTML string, err error) http.Handler {
				return nil
			}),
			site.NewDirectoryDirEntryHandler(func(s *site.Site, dir site.Metadata, children []site.Metadata) http.Handler {
				return nil
			}),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedMenu := []site.MenuItem{
		{URL: "/", Title: "Home", Children: []site.MenuItem{
			{URL: "/a", Title: "A", Children: []site.MenuItem{
				{URL: "/a/1", Title: "1", Children: []site.MenuItem{
					{URL: "/a/1/aa", Title: "Aa"},
				}},
				{URL: "/a/2", Title: "2"},
			}},
			{URL: "/b", Title: "B"},
			{URL: "/c", Title: "C"},
			{URL: "/d", Title: "D", Children: []site.MenuItem{
				{URL: "/d/3", Title: "3", Children: []site.MenuItem{
					{URL: "/d/3/4", Title: "4"},
				}},
			}},
		}},
	}

	if diff := cmp.Diff(expectedMenu, s.Menu()); diff != "" {
		t.Fatalf("unexpected menu (-want +got):\n%s", diff)
	}
}
