package notify

import "github.com/google/uuid"

// newID returns a time-ordered id (UUIDv7: sortable, so queue and log scans stay chronological).
func newID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}
