package site

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"go.abhg.dev/goldmark/frontmatter"
	"go.abhg.dev/goldmark/toc"
)

var _ Content = &Markdown{}

func NewMarkdownDirEntryHandler(handler func(site *Site, page Metadata, toc []MenuItem, outputHTML string, err error) http.Handler) DirEntryHandler {
	return func(s *Site, dirFS fs.FS, path string, d fs.DirEntry) (url string, content Content, ok bool, err error) {
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return url, content, false, nil
		}
		md := &Markdown{
			Site:    s,
			fs:      dirFS,
			path:    path,
			Handler: handler,
		}
		if _, _, err = md.Read(); err != nil {
			return url, content, false, fmt.Errorf("failed to read markdown file: %w", err)
		}
		return filePathToURL(path), md, true, nil
	}
}

type Markdown struct {
	Site    *Site
	fs      fs.FS
	path    string
	m       Metadata
	toc     []MenuItem
	mu      sync.Mutex
	lastMod time.Time
	Handler func(site *Site, page Metadata, toc []MenuItem, outputHTML string, err error) http.Handler
}

func (p *Markdown) Metadata() (m Metadata) {
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

func (p *Markdown) Text() (s string, err error) {
	src, node, err := p.Read()
	if err != nil {
		return "", fmt.Errorf("failed to read markdown file: %w", err)
	}

	var buf bytes.Buffer
	extractText(&buf, src, node)
	return buf.String(), nil
}

func (p *Markdown) TOC() (items []MenuItem) {
	return items
}

func (p *Markdown) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	outputHTML, err := p.HTML()
	p.Handler(p.Site, p.Metadata(), p.toc, outputHTML, err).ServeHTTP(w, r)
}

func (p *Markdown) Read() (src []byte, node ast.Node, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	f, err := p.fs.Open(p.path)
	if err != nil {
		return nil, nil, fmt.Errorf("could not open file: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to stat file: %w", err)
	}
	p.lastMod = fi.ModTime()

	if src, err = io.ReadAll(f); err != nil {
		return nil, nil, fmt.Errorf("failed to Read file: %w", err)
	}

	ctx := parser.NewContext()
	node = gmParser.Parse(text.NewReader(src), parser.WithContext(ctx))

	if d := frontmatter.Get(ctx); d != nil {
		if err = d.Decode(&p.m); err != nil {
			return nil, nil, fmt.Errorf("failed to decode frontmatter: %w", err)
		}
	}
	p.setMetadataDefaults()

	tree, err := toc.Inspect(node, src)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to inspect toc: %w", err)
	}
	p.toc = convertToMenuItem(tree.Items)

	return src, node, nil
}

func convertToMenuItem(items toc.Items) (tm []MenuItem) {
	tm = make([]MenuItem, len(items))
	for i, item := range items {
		tm[i] = MenuItem{
			Title:    string(item.Title),
			URL:      fmt.Sprintf("#%s", item.ID),
			Children: convertToMenuItem(item.Items),
		}
	}
	return tm
}

func (p *Markdown) setMetadataDefaults() {
	if p.m.MimeType == "" {
		p.m.MimeType = "text/html; charset=utf-8"
	}
	if p.m.LastMod.Equal(time.Time{}) {
		p.m.LastMod = p.lastMod
	}
	if p.m.Title == "" {
		base, fn := filepath.Split(p.path)
		if fn == "index.md" {
			fn = filepath.Base(base)
		}
		if fn == "." {
			fn = "Home"
		}
		if strings.HasSuffix(fn, ".md") {
			fn = strings.TrimSuffix(fn, ".md")
		}
		p.m.Title = englishCases.String(fn)
	}
	if p.m.URL == "" {
		p.m.URL = filePathToURL(p.path)
	}
}

var gmExtensions = []goldmark.Extender{
	extension.Table,
	&frontmatter.Extender{},
}
var gm = goldmark.New(
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	goldmark.WithExtensions(gmExtensions...),
)
var gmParser = gm.Parser()
var gmRenderer = gm.Renderer()

func (p *Markdown) HTML() (s string, err error) {
	src, node, err := p.Read()
	if err != nil {
		return s, fmt.Errorf("failed to read markdown file: %w", err)
	}
	buf := new(bytes.Buffer)
	if err = gmRenderer.Render(buf, src, node); err != nil {
		return s, fmt.Errorf("failed to render markdown file: %w", err)
	}
	return buf.String(), nil
}
