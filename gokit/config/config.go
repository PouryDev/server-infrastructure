// Package config reads typed settings from environment variables.
//
// A Loader collects every problem instead of stopping at the first one, so a misconfigured
// deployment reports all of its missing or malformed variables at once.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Loader reads variables from a lookup function (os.LookupEnv by default).
type Loader struct {
	lookup func(string) (string, bool)
	errs   []error
}

// FromEnv returns a Loader backed by the process environment.
func FromEnv() *Loader { return &Loader{lookup: os.LookupEnv} }

// FromMap returns a Loader backed by a map (for tests).
func FromMap(m map[string]string) *Loader {
	return &Loader{lookup: func(k string) (string, bool) { v, ok := m[k]; return v, ok }}
}

func (l *Loader) raw(key string) (string, bool) {
	v, ok := l.lookup(key)
	v = strings.TrimSpace(v)
	return v, ok && v != ""
}

func (l *Loader) fail(key, format string, args ...any) {
	l.errs = append(l.errs, fmt.Errorf("%s: %s", key, fmt.Sprintf(format, args...)))
}

// Err returns every problem found so far, or nil.
func (l *Loader) Err() error { return errors.Join(l.errs...) }

func (l *Loader) String(key, def string) string {
	if v, ok := l.raw(key); ok {
		return v
	}
	return def
}

// Required records an error when the variable is missing.
func (l *Loader) Required(key string) string {
	v, ok := l.raw(key)
	if !ok {
		l.fail(key, "is required")
	}
	return v
}

func (l *Loader) Int(key string, def int) int {
	v, ok := l.raw(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.fail(key, "%q is not an integer", v)
		return def
	}
	return n
}

func (l *Loader) Bool(key string, def bool) bool {
	v, ok := l.raw(key)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		l.fail(key, "%q is not a boolean", v)
		return def
	}
	return b
}

func (l *Loader) Duration(key string, def time.Duration) time.Duration {
	v, ok := l.raw(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		l.fail(key, "%q is not a duration (examples: 500ms, 30s, 15m)", v)
		return def
	}
	return d
}

// List splits a comma-separated variable; blank items are dropped.
func (l *Loader) List(key string, def []string) []string {
	v, ok := l.raw(key)
	if !ok {
		return def
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// OneOf returns the value when it is one of the allowed ones, otherwise records an error.
func (l *Loader) OneOf(key, def string, allowed ...string) string {
	v := l.String(key, def)
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	l.fail(key, "%q is not one of %s", v, strings.Join(allowed, ", "))
	return def
}

// MinLen records an error when value is shorter than n bytes (for secrets).
func (l *Loader) MinLen(key, value string, n int) string {
	if len(value) < n {
		l.fail(key, "must be at least %d characters", n)
	}
	return value
}
