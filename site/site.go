package site

import (
	"fmt"
	"io/fs"
	"iter"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Site contains all of the static content for a site.
// It implements the http.Handler interface.
//
// Content is stored in a map, with the key being the URL path.
//
// To customise content handling, pass a slice of DirEntryHandler functions to the New function.
type Site struct {
	Log     *slog.Logger
	Title   string
	BaseURL string
	content map[string]Content
}

type Content interface {
	Metadata() (m Metadata)
	Text() (text string, err error)
	TOC() (toc []MenuItem)
	ServeHTTP(w http.ResponseWriter, r *http.Request)
}

type Metadata struct {
	// URL is the relative URL of the content, e.g. /about.
	URL string
	// Title of the content.
	Title string
	// Summary of the content.
	Summary string
	// MimeType of the content.
	MimeType string    `yaml:"mimeType"`
	LastMod  time.Time `yaml:"lastMod"`
	// Type of the data.
	Type string
	Data any
}

type SiteArgs struct {
	Log             *slog.Logger
	Dir             fs.FS
	BaseURL         string
	Title           string
	ContentHandlers []DirEntryHandler
}

// DirEntryHandler is a function that can be used to handle a directory entry.
// It returns the URL, Content and whether the entry is valid.
type DirEntryHandler func(s *Site, dirFS fs.FS, path string, d fs.DirEntry) (url string, content Content, ok bool, err error)

// New creates a new site from the given io.FS.
// Create a new FS using os.DirFS and pass it to this function.
func New(args SiteArgs) (site *Site, err error) {
	if len(args.ContentHandlers) == 0 {
		return nil, fmt.Errorf("no content handlers provided")
	}
	if args.Log == nil {
		args.Log = slog.Default()
	}
	if args.Title == "" {
		args.Title = "ragmark site"
	}
	if args.BaseURL == "" {
		args.BaseURL = "http://localhost:1414/"
	}

	site = &Site{
		Log:     args.Log,
		BaseURL: args.BaseURL,
		Title:   args.Title,
		content: map[string]Content{},
	}

	err = fs.WalkDir(args.Dir, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("failed to walk directory: %w", err)
		}
		for _, h := range args.ContentHandlers {
			url, content, ok, err := h(site, args.Dir, path, d)
			if err != nil {
				return fmt.Errorf("failed to handle directory entry: %w", err)
			}
			if !ok {
				continue
			}
			site.Add(url, content)
			return nil
		}
		return fmt.Errorf("no content handler found for: %q", path)
	})

	return site, err
}

func (s *Site) Add(path string, content Content) {
	s.content[path] = content
}

// Content returns a sequence of paths and content.
func (s Site) Content() iter.Seq2[string, Content] {
	return func(yield func(string, Content) bool) {
		for _, k := range slices.Sorted(maps.Keys(s.content)) {
			if !yield(k, s.content[k]) {
				return
			}
		}
	}
}

func (s Site) GetContent(url string) (c Content, ok bool) {
	c, ok = s.content[url]
	return c, ok
}

func (s Site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.Log.Info("serving page", slog.String("url", r.URL.String()))
	handler, ok := s.content[r.URL.Path]
	if !ok {
		s.Log.Info("page not found", slog.String("url", r.URL.String()))
		http.NotFound(w, r)
		return
	}
	handler.ServeHTTP(w, r)
}

type MenuItem struct {
	URL      string
	Title    string
	Children []MenuItem
}

func (m MenuItem) sort() {
	slices.SortFunc(m.Children, func(a, b MenuItem) int {
		return strings.Compare(a.URL, b.URL)
	})
	for _, c := range m.Children {
		c.sort()
	}
}

func (s Site) Menu() (menu []MenuItem) {
	var urls []string
	urlToMenuItem := map[string]*MenuItem{}
	for url, content := range s.Content() {
		m := content.Metadata()
		urls = append(urls, url)
		urlToMenuItem[url] = &MenuItem{
			URL:   url,
			Title: m.Title,
		}
	}
	// Sort URLs by length descending.
	slices.SortFunc(urls, func(a, b string) int {
		if len(a) == len(b) {
			return strings.Compare(a, b)
		}
		if len(a) < len(b) {
			return 1
		}
		return -1
	})
	// For each URL, add it to the appropriate parent.
	moved := map[string]bool{}
	for i, current := range urls {
		for j, candidateParentURL := range urls {
			if j == i {
				continue
			}
			if strings.HasPrefix(current, candidateParentURL) && !moved[current] {
				currentMenuItem := urlToMenuItem[current]
				urlToMenuItem[candidateParentURL].Children = append(urlToMenuItem[candidateParentURL].Children, *currentMenuItem)
				moved[current] = true
				break
			}
		}
	}
	// Remove moved items from the top level.
	for k := range moved {
		delete(urlToMenuItem, k)
	}
	// Sort the top level items.
	for _, m := range urlToMenuItem {
		m.sort()
		menu = append(menu, *m)
	}
	return menu
}
