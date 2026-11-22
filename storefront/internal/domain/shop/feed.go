package shop

import "time"

// FeedQuery expresses a bounded catalog read independent of any marketplace.
type FeedQuery struct {
	IDs        []string
	Slugs      []string
	VariantIDs []string
	Sort       string
	BeforeID   string
	Offset     int
}
type FeedPage struct {
	Products []Product // Each row contains exactly one hydrated variant.
	Total    int
}
type SyncJob struct {
	ProductID  string
	Generation int64
	Attempts   int
}
type SyncStatus struct {
	Pending     int        `json:"pending"`
	LastSuccess *time.Time `json:"lastSuccess"`
}
