package delivery

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestTransferProgressCountsReadBytesAndPropagatesPersistenceFailure(t *testing.T) {
	var seen int64
	r := &transferReader{source: strings.NewReader("actual-data"), report: func(n int64) error { seen = n; return nil }}
	n, err := io.Copy(io.Discard, r)
	if err != nil || n != 11 || r.bytes != 11 || seen != 11 {
		t.Fatal(n, err, r.bytes, seen)
	}
	failure := errors.New("progress storage failed")
	r = &transferReader{source: strings.NewReader("x"), report: func(int64) error { return failure }}
	if _, err = io.Copy(io.Discard, r); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}
