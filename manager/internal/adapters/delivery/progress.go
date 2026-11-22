package delivery

import (
	"io"
	"time"
)

// Counts bytes actually consumed by SSH, without pretending the image's
// uncompressed size is the (different) size of a docker-save tar stream.
type transferReader struct {
	source io.Reader
	bytes  int64
	last   time.Time
	report func(int64) error
}

func (r *transferReader) Read(p []byte) (int, error) {
	n, err := r.source.Read(p)
	r.bytes += int64(n)
	if (n > 0 && (r.last.IsZero() || time.Since(r.last) >= time.Second)) || err == io.EOF {
		if reportErr := r.report(r.bytes); reportErr != nil {
			return n, reportErr
		}
		r.last = time.Now()
	}
	return n, err
}
