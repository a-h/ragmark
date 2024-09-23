package site

import (
	"io/fs"
	"net/http"
	"strings"
)

var _ Content = Directory{}

func NewDirectoryDirEntryHandler(handlerFunc func(s *Site, dir Metadata, children []Metadata) http.Handler) DirEntryHandler {
	return func(s *Site, dirFS fs.FS, path string, d fs.DirEntry) (url string, content Content, ok bool, err error) {
		if !d.IsDir() {
			return url, content, false, nil
		}
		url = filePathToURL(path)
		urlPathSegments := strings.Split(url, "/")
		title := "Home"
		if len(urlPathSegments) > 0 {
			title = englishCases.String(urlPathSegments[len(urlPathSegments)-1])
		}
		return filePathToURL(path), Directory{
			Site:        s,
			URL:         url,
			Title:       title,
			HandlerFunc: handlerFunc,
		}, true, nil
	}
}

type Directory struct {
	Site  *Site
	URL   string
	Title string
	// HandlerFunc is a function that returns an http.Handler that will render the directory.
	// The handler will be passed the children of the directory.
	HandlerFunc func(site *Site, dir Metadata, children []Metadata) http.Handler
}

func (d Directory) Metadata() (m Metadata) {
	return Metadata{
		URL:   d.URL,
		Title: d.Title,
	}
}

func (d Directory) Text() (text string, err error) {
	return "", nil
}

func (d Directory) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var childMetadata []Metadata
	for url, content := range d.Site.Content() {
		if !strings.HasPrefix(url, d.URL) || url == d.URL {
			continue
		}
		childMetadata = append(childMetadata, content.Metadata())
	}
	handler := d.HandlerFunc(d.Site, d.Metadata(), childMetadata)
	handler.ServeHTTP(w, r)
}
