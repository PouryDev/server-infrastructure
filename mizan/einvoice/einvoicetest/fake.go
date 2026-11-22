// Package einvoicetest is an in-memory einvoice.Provider for tests: it hands out reference numbers and answers
// inquiries with the verdict the test chose.
package einvoicetest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/PouryDev/server-infrastructure/mizan/einvoice"
)

// Provider is a fake provider.
type Provider struct {
	mu        sync.Mutex
	n         int
	Packets   []einvoice.Packet // everything ever submitted
	byRef     map[string]einvoice.Packet
	Verdicts  map[string]einvoice.Result // by trace id; the default verdict is pending
	SubmitErr error                      // returned by Submit when set
	Lim       einvoice.Limits
}

// New creates the fake with small limits so batching is easy to exercise.
func New() *Provider {
	return &Provider{byRef: map[string]einvoice.Packet{}, Verdicts: map[string]einvoice.Result{}, Lim: einvoice.Limits{Submit: 1000, Inquire: 100}}
}

func (p *Provider) Limits() einvoice.Limits { return p.Lim }

func (p *Provider) Submit(_ context.Context, packets []einvoice.Packet) ([]einvoice.Receipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.SubmitErr != nil {
		return nil, p.SubmitErr
	}
	out := make([]einvoice.Receipt, 0, len(packets))
	for _, pk := range packets {
		p.n++
		ref := fmt.Sprintf("REF-%06d", p.n)
		p.Packets = append(p.Packets, pk)
		p.byRef[ref] = pk
		out = append(out, einvoice.Receipt{TraceID: pk.TraceID, ReferenceNumber: ref})
	}
	return out, nil
}

// Decide sets the verdict the provider will give for a trace id.
func (p *Provider) Decide(traceID string, r einvoice.Result) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Verdicts[traceID] = r
}

func (p *Provider) Inquire(_ context.Context, refs []string, _ time.Time) ([]einvoice.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]einvoice.Result, 0, len(refs))
	for _, ref := range refs {
		pk, ok := p.byRef[ref]
		if !ok {
			out = append(out, einvoice.Result{ReferenceNumber: ref, Outcome: einvoice.OutcomeNotFound})
			continue
		}
		r, ok := p.Verdicts[pk.TraceID]
		if !ok {
			r = einvoice.Result{Outcome: einvoice.OutcomePending}
		}
		r.ReferenceNumber, r.TraceID = ref, pk.TraceID
		out = append(out, r)
	}
	return out, nil
}
