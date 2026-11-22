package query

// PageMeta describes the position of a page in the full result.
type PageMeta struct {
	Page       int    `json:"page"`
	PageSize   int    `json:"page_size"`
	HasNext    bool   `json:"has_next"`
	Total      *int64 `json:"total,omitempty"`       // absent when counting was skipped
	TotalPages *int   `json:"total_pages,omitempty"` // absent when counting was skipped
}

// Page is a list response.
type Page[T any] struct {
	Items []T      `json:"items"`
	Meta  PageMeta `json:"meta"`
}

// NewPage assembles a Page; total < 0 means the total is unknown.
func NewPage[T any](items []T, page, pageSize int, hasNext bool, total int64) Page[T] {
	if items == nil {
		items = []T{}
	}
	m := PageMeta{Page: page, PageSize: pageSize, HasNext: hasNext}
	if total >= 0 {
		t := total
		pages := int((total + int64(pageSize) - 1) / int64(pageSize))
		m.Total, m.TotalPages = &t, &pages
	}
	return Page[T]{Items: items, Meta: m}
}

// MapPage converts the items of a page (e.g. rows to domain entities) and keeps the metadata.
func MapPage[T, U any](p Page[T], fn func(T) U) Page[U] {
	out := make([]U, len(p.Items))
	for i, it := range p.Items {
		out[i] = fn(it)
	}
	return Page[U]{Items: out, Meta: p.Meta}
}
