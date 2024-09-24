package chat

import (
	"context"
	"io"
	"net/http"

	"github.com/a-h/ragmark/site"
	"github.com/a-h/ragmark/templates"
	"github.com/a-h/templ"
)

func NewFormHandler(s *site.Site) FormHandler {
	return FormHandler{
		Site: s,
	}
}

type FormHandler struct {
	Site *site.Site
}

func (h FormHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	left := templates.Left(h.Site)
	middle := templates.ChatForm(r.FormValue("prompt"))
	right := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		return nil
	})
	templ.Handler(templates.Page(left, middle, right)).ServeHTTP(w, r)
}
