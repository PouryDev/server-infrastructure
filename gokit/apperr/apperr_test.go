package apperr

import (
	"errors"
	"fmt"
	"testing"
)

func TestKindOf(t *testing.T) {
	cause := errors.New("boom")
	wrapped := fmt.Errorf("ctx: %w", Wrap(KindConflict, "dup", "already exists", cause))

	if got := KindOf(wrapped); got != KindConflict {
		t.Fatalf("KindOf = %v, want conflict", got)
	}
	if got := KindOf(cause); got != KindInternal {
		t.Fatalf("unclassified error must be internal, got %v", got)
	}
	if !errors.Is(wrapped, cause) {
		t.Fatal("cause must stay reachable through Unwrap")
	}
	if !errors.Is(wrapped, New(KindConflict, "dup", "")) {
		t.Fatal("errors.Is must match on kind and code")
	}
}

func TestDefaults(t *testing.T) {
	e := NotFound("user")
	if e.Code != "not_found" || e.Message != "user not found" {
		t.Fatalf("unexpected %+v", e)
	}
	if !Is(e, KindNotFound) || Is(e, KindInvalid) {
		t.Fatal("Is mismatch")
	}
}
