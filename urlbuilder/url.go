package urlbuilder

import (
	"net/url"
	"path"
)

type URLBuilder struct {
	u *url.URL
}

func (ub *URLBuilder) Query(key string, value string) *URLBuilder {
	q := ub.u.Query()
	q.Add(key, value)
	ub.u.RawQuery = q.Encode()
	return ub
}

func (ub *URLBuilder) String() string {
	return ub.u.String()
}

func newURLBuilder() *URLBuilder {
	return &URLBuilder{
		u: &url.URL{},
	}
}

func Path(segment string) *URLBuilder {
	ub := newURLBuilder()
	ub.u.Path = path.Join(ub.u.Path, segment)
	return ub
}
