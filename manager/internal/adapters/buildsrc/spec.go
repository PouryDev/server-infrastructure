// Package buildsrc resolves owner-registered local project sources and reads their
// ship.conf-style build specification without executing any shell code.
package buildsrc

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type Spec struct {
	App         string
	ComposeFile string
	Platform    string
	KeepTags    int
	Images      []domain.BuildSpec
	ArgNames    []string
	Literals    map[string]string
	Network     string
}

var (
	appPattern      = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	servicePattern  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,99}$`)
	argNamePattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	platformPattern = regexp.MustCompile(`^linux/(amd64|arm64)$`)
	keyPattern      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type word struct {
	text      string
	expansion bool
}
type lexer struct {
	s   string
	pos int
}

func (l *lexer) eof() bool { return l.pos >= len(l.s) }
func (l *lexer) skipSpace(newlines bool) {
	for !l.eof() {
		c := l.s[l.pos]
		if c == ' ' || c == '\t' || c == '\r' || (newlines && c == '\n') {
			l.pos++
		} else if c == '#' {
			for !l.eof() && l.s[l.pos] != '\n' {
				l.pos++
			}
		} else {
			return
		}
	}
}
func (l *lexer) skipLine() {
	for !l.eof() && l.s[l.pos] != '\n' {
		l.pos++
	}
}

// readWord reads one shell word made of bare text and quoted segments.
func (l *lexer) readWord(inArray bool) (word, error) {
	var w word
	for !l.eof() {
		c := l.s[l.pos]
		switch {
		case c == '\'':
			end := strings.IndexByte(l.s[l.pos+1:], '\'')
			if end < 0 {
				return w, fmt.Errorf("unterminated quote")
			}
			w.text += l.s[l.pos+1 : l.pos+1+end]
			l.pos += end + 2
		case c == '"':
			l.pos++
			for {
				if l.eof() {
					return w, fmt.Errorf("unterminated quote")
				}
				d := l.s[l.pos]
				if d == '"' {
					l.pos++
					break
				}
				if d == '\\' && l.pos+1 < len(l.s) {
					w.text += string(l.s[l.pos+1])
					l.pos += 2
					continue
				}
				if d == '$' || d == '`' {
					w.expansion = true
				}
				w.text += string(d)
				l.pos++
			}
		case c == '$' || c == '`':
			w.expansion = true
			w.text += string(c)
			l.pos++
		case c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == ';' || (inArray && c == ')'):
			return w, nil
		case c == '(' || c == ')' || c == '|' || c == '&' || c == '<' || c == '>':
			return w, fmt.Errorf("unsupported shell syntax")
		default:
			w.text += string(c)
			l.pos++
		}
	}
	return w, nil
}

type assignment struct {
	scalar *word
	array  []word
}

// parse walks top-level NAME=value / NAME=(...) statements. Anything else is
// skipped line by line, so unrelated shell in a ship.conf is never interpreted.
func parse(src string) (map[string]assignment, error) {
	l := &lexer{s: src}
	out := map[string]assignment{}
	for {
		l.skipSpace(true)
		if l.eof() {
			return out, nil
		}
		start := l.pos
		for !l.eof() && (l.s[l.pos] == '_' || l.s[l.pos] >= 'a' && l.s[l.pos] <= 'z' || l.s[l.pos] >= 'A' && l.s[l.pos] <= 'Z' || l.s[l.pos] >= '0' && l.s[l.pos] <= '9') {
			l.pos++
		}
		key := l.s[start:l.pos]
		if key == "export" && !l.eof() && l.s[l.pos] == ' ' {
			l.skipSpace(false)
			continue
		}
		if key == "" || l.eof() || l.s[l.pos] != '=' || !keyPattern.MatchString(key) {
			l.pos = start
			l.skipLine()
			continue
		}
		l.pos++
		var a assignment
		if !l.eof() && l.s[l.pos] == '(' {
			l.pos++
			for {
				l.skipSpace(true)
				if l.eof() {
					return nil, fmt.Errorf("unterminated array in %s", key)
				}
				if l.s[l.pos] == ')' {
					l.pos++
					break
				}
				w, err := l.readWord(true)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", key, err)
				}
				a.array = append(a.array, w)
			}
		} else {
			w, err := l.readWord(false)
			if err != nil {
				// Unsupported syntax in an unrelated variable is not our concern.
				l.skipLine()
				continue
			}
			a.scalar = &w
		}
		out[key] = a
	}
}

func scalar(m map[string]assignment, key string) (string, bool, error) {
	a, ok := m[key]
	if !ok {
		return "", false, nil
	}
	if a.scalar == nil || a.scalar.expansion {
		return "", true, fmt.Errorf("%s must be a plain value without shell expansion", key)
	}
	return a.scalar.text, true, nil
}

func cleanRel(p string) (string, error) {
	if p == "" || path.IsAbs(p) || strings.ContainsAny(p, "\x00\n") {
		return "", fmt.Errorf("invalid relative path")
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("path escapes the project directory")
	}
	return c, nil
}

// ParseSpec reads APP, COMPOSE_FILE, IMAGES, PLATFORM, KEEP_TAGS and BUILD_ARGS.
func ParseSpec(src string) (Spec, error) {
	m, err := parse(src)
	if err != nil {
		return Spec{}, err
	}
	spec := Spec{ComposeFile: "deploy/compose.yaml", Platform: "linux/amd64", KeepTags: 3, Literals: map[string]string{}}
	if spec.App, _, err = scalar(m, "APP"); err != nil {
		return spec, err
	}
	if !appPattern.MatchString(spec.App) {
		return spec, fmt.Errorf("APP must match ^[a-z][a-z0-9-]{1,30}$")
	}
	if v, ok, err := scalar(m, "COMPOSE_FILE"); err != nil {
		return spec, err
	} else if ok {
		if spec.ComposeFile, err = cleanRel(v); err != nil {
			return spec, fmt.Errorf("COMPOSE_FILE: %w", err)
		}
	}
	if v, ok, err := scalar(m, "PLATFORM"); err != nil {
		return spec, err
	} else if ok {
		if !platformPattern.MatchString(v) {
			return spec, fmt.Errorf("PLATFORM must be linux/amd64 or linux/arm64")
		}
		spec.Platform = v
	}
	if v, ok, err := scalar(m, "KEEP_TAGS"); err != nil {
		return spec, err
	} else if ok {
		n, e := strconv.Atoi(v)
		if e != nil || n < 2 || n > 20 {
			return spec, fmt.Errorf("KEEP_TAGS must be between 2 and 20")
		}
		spec.KeepTags = n
	}
	images, ok := m["IMAGES"]
	if !ok || len(images.array) == 0 || len(images.array) > 20 {
		return spec, fmt.Errorf("IMAGES must list 1-20 service=context entries")
	}
	seen := map[string]bool{}
	for _, w := range images.array {
		if w.expansion {
			return spec, fmt.Errorf("IMAGES cannot use shell expansion")
		}
		service, rest, found := strings.Cut(w.text, "=")
		if !found || !servicePattern.MatchString(service) || seen[service] {
			return spec, fmt.Errorf("invalid IMAGES entry")
		}
		seen[service] = true
		context, dockerfile, _ := strings.Cut(rest, ":")
		item := domain.BuildSpec{Service: service}
		if item.Context, err = cleanRel(context); err != nil {
			return spec, fmt.Errorf("IMAGES %s context: %w", service, err)
		}
		if dockerfile != "" {
			if item.Dockerfile, err = cleanRel(dockerfile); err != nil {
				return spec, fmt.Errorf("IMAGES %s dockerfile: %w", service, err)
			}
		}
		spec.Images = append(spec.Images, item)
	}
	if args, ok := m["BUILD_ARGS"]; ok {
		for i := 0; i < len(args.array); i++ {
			w := args.array[i]
			if w.expansion {
				return spec, fmt.Errorf("BUILD_ARGS cannot use shell expansion")
			}
			next := func() (string, error) {
				if i+1 >= len(args.array) || args.array[i+1].expansion {
					return "", fmt.Errorf("BUILD_ARGS: missing value after %s", w.text)
				}
				i++
				return args.array[i].text, nil
			}
			switch {
			case w.text == "--build-arg":
				v, err := next()
				if err != nil {
					return spec, err
				}
				name, value, hasValue := strings.Cut(v, "=")
				if !argNamePattern.MatchString(name) {
					return spec, fmt.Errorf("BUILD_ARGS: invalid build argument name")
				}
				if hasValue {
					spec.Literals[name] = value
				} else {
					spec.ArgNames = append(spec.ArgNames, name)
				}
			case w.text == "--network" || strings.HasPrefix(w.text, "--network="):
				v := strings.TrimPrefix(w.text, "--network=")
				if w.text == "--network" {
					var err error
					if v, err = next(); err != nil {
						return spec, err
					}
				}
				if v != "host" && v != "default" {
					return spec, fmt.Errorf("BUILD_ARGS: only --network host or default is supported")
				}
				spec.Network = v
			default:
				return spec, fmt.Errorf("BUILD_ARGS: unsupported option %q", w.text)
			}
		}
	}
	return spec, nil
}
