// Package trail holds the two side effects every use case may have besides changing its own data: a
// domain event for other services (delivered through the transactional outbox) and an audit record.
// Use cases depend on these small interfaces; outbox.Writer and audit.Log implement them.
package trail

import "context"

// Event is announced to other services, e.g. Topic "payment.paid".
type Event struct {
	Topic         string
	AggregateType string
	AggregateID   string
	Payload       any
}

// Emitter stores an event in the caller's transaction.
type Emitter interface {
	Emit(ctx context.Context, e Event) error
}

// Entry is one audit record: who did what to which entity.
type Entry struct {
	Actor      string // user id, "system" or a service name
	Action     string // e.g. "invoice.issue"
	EntityType string
	EntityID   string
	Details    map[string]any
}

// Recorder stores an audit entry in the caller's transaction.
type Recorder interface {
	Record(ctx context.Context, e Entry) error
}

// Discard is an Emitter and Recorder that drops everything (tests, or projects that do not want events).
type Discard struct{}

func (Discard) Emit(context.Context, Event) error   { return nil }
func (Discard) Record(context.Context, Entry) error { return nil }
