package site

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"
)

var _ Content = Markdown{}

func NewMarkdownDirEntryHandler(handler func(site *Site, page Metadata, outputHTML string, err error) http.Handler) DirEntryHandler {
	return func(s *Site, dirFS fs.FS, path string, d fs.DirEntry) (url string, content Content, ok bool, err error) {
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return url, content, false, nil
		}
		m, err := getMetadata(dirFS, path)
		if err != nil {
			return url, content, false, err
		}
		content = Markdown{
			Site:    s,
			fs:      dirFS,
			path:    path,
			m:       m,
			Handler: handler,
		}
		return filePathToURL(path), content, true, nil
	}
}

func filePathToURL(fp string) (u string) {
	if fp == "." {
		return "/"
	}
	if fp == "index.md" {
		return "/"
	}
	list := strings.Split(fp, string(os.PathSeparator))

	fileName := list[len(list)-1]
	// If it's a markdown file, remove the extension.
	list[len(list)-1] = strings.TrimSuffix(fileName, ".md")
	// If it's an index file, remove the filename.
	if fileName == "index.md" {
		list = list[:len(list)-1]
	}

	// URL escape the paths.
	for i, v := range list {
		list[i] = url.PathEscape(v)
	}

	return "/" + strings.Join(list, "/")
}

type Markdown struct {
	Site    *Site
	fs      fs.FS
	path    string
	m       Metadata
	Handler func(site *Site, page Metadata, outputHTML string, err error) http.Handler
}

func (p Markdown) Metadata() (m Metadata) {
	return p.m
}

func extractText(buf *bytes.Buffer, src []byte, node ast.Node) {
	for n := node.FirstChild(); n != nil; n = n.NextSibling() {
		newLine := "\n\n"
		if _, isListItem := n.(*ast.ListItem); isListItem {
			newLine = "\n"
		}
		if n.Type() == ast.TypeInline {
			newLine = ""
		}
		switch n := n.(type) {
		case *ast.Text:
			segment := n.Segment
			buf.Write(segment.Value(src))
		default:
			extractText(buf, src, n)
			buf.WriteString(newLine)
		}
	}
}

func (p Markdown) Text() (s string, err error) {
	src, node, err := p.read()
	if err != nil {
		return "", fmt.Errorf("failed to read markdown file: %w", err)
	}

	var buf bytes.Buffer
	extractText(&buf, src, node)
	return buf.String(), nil
}

func getMetadata(fs fs.FS, path string) (m Metadata, err error) {
	f, err := fs.Open(path)
	if err != nil {
		return m, fmt.Errorf("could not open file: %w", err)
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	var metadataBuffer bytes.Buffer
	var started bool
scan:
	for s.Scan() {
		if s.Err() != nil {
			return m, fmt.Errorf("failed to read file: %w", err)
		}
		if s.Text() == "---" {
			if !started {
				started = true
				continue
			} else {
				break scan
			}
		}
		if !started {
			continue
		}
		metadataBuffer.WriteString(s.Text())
		metadataBuffer.WriteString("\n")
	}

	if err = yaml.Unmarshal(metadataBuffer.Bytes(), &m); err != nil {
		return m, fmt.Errorf("failed to unmarshal metadata %q: %w", string(metadataBuffer.Bytes()), err)
	}

	if m.MimeType == "" {
		m.MimeType = "text/html; charset=utf-8"
	}
	if m.LastMod.Equal(time.Time{}) {
		fi, err := f.Stat()
		if err != nil {
			return m, fmt.Errorf("failed to stat file: %w", err)
		}
		m.LastMod = fi.ModTime()
	}
	if m.Title == "" {
		base, fn := filepath.Split(path)
		if fn == "index.md" {
			fn = filepath.Base(base)
		}
		if fn == "." {
			fn = "Home"
		}
		if strings.HasSuffix(fn, ".md") {
			fn = strings.TrimSuffix(fn, ".md")
		}
		m.Title = fn
	}
	if m.URL == "" {
		m.URL = filePathToURL(path)
	}

	return m, nil
}

func (p Markdown) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	outputHTML, err := p.HTML()
	p.Handler(p.Site, p.Metadata(), outputHTML, err).ServeHTTP(w, r)
}

func (p Markdown) read() (src []byte, node ast.Node, err error) {
	f, err := p.fs.Open(p.path)
	if err != nil {
		return nil, nil, fmt.Errorf("could not open file: %w", err)
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	var contentBuffer bytes.Buffer
	var started bool
	for s.Scan() {
		if s.Err() != nil {
			return nil, nil, fmt.Errorf("failed to read file: %w", err)
		}
		if s.Text() == "---" {
			started = !started
			continue
		}
		if started {
			continue
		}
		contentBuffer.WriteString(s.Text())
		contentBuffer.WriteString("\n")
	}

	return contentBuffer.Bytes(), gmParser.Parse(text.NewReader(contentBuffer.Bytes())), nil
}

var gm = goldmark.New(goldmark.WithExtensions(extension.Table))
var gmParser = gm.Parser()
var gmRenderer = gm.Renderer()

func (p Markdown) HTML() (s string, err error) {
	buf := new(bytes.Buffer)
	src, node, err := p.read()
	if err != nil {
		return s, fmt.Errorf("failed to read markdown file: %w", err)
	}
	if err = gmRenderer.Render(buf, src, node); err != nil {
		return s, fmt.Errorf("failed to render markdown file: %w", err)
	}
	return buf.String(), nil
}
