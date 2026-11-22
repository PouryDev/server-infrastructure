package api

import (
	"net/url"

	"github.com/PouryDev/server-infrastructure/gokit/query"
)

func mapPage[T, U any](p query.Page[T], fn func(T) U) query.Page[U] { return query.MapPage(p, fn) }

func parseForm(body string) (map[string]string, error) {
	v, err := url.ParseQuery(body)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(v))
	for k, vs := range v {
		out[k] = vs[0]
	}
	return out, nil
}
