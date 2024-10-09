package site

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yuin/goldmark/text"
)

func TestMarkdownRendering(t *testing.T) {
	t.Run("can render d2 diagrams", func(t *testing.T) {
		var md = "```d2" + `
a -> b
` + "```"
		w := new(bytes.Buffer)
		n := gmParser.Parse(text.NewReader([]byte(md)))
		if err := gmRenderer.Render(w, []byte(md), n); err != nil {
			t.Fatalf("failed to render markdown: %v", err)
		}
		html := w.String()
		if !strings.Contains(html, `svg id="d2-svg"`) {
			t.Errorf("d2 diagram not found in HTML: %q", html)
		}
	})
}
