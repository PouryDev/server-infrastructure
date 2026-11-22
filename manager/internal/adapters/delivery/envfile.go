package delivery

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

// envDoc edits a docker-compose env_file while preserving comments, blank lines,
// ordering, unknown lines and the line-ending style.
type envDoc struct {
	lines []string
	crlf  bool
}

var (
	envAssign = regexp.MustCompile(`^(\s*(?:export\s+)?)([A-Za-z_][A-Za-z0-9_.-]*)(\s*=\s*)(.*)$`)
	envBare   = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)
)

func parseEnvDoc(data []byte) *envDoc {
	text := string(data)
	d := &envDoc{crlf: strings.Contains(text, "\r\n")}
	if text == "" {
		return d
	}
	text = strings.TrimSuffix(text, "\n")
	for _, line := range strings.Split(text, "\n") {
		d.lines = append(d.lines, strings.TrimSuffix(line, "\r"))
	}
	return d
}

func (d *envDoc) bytes() []byte {
	if len(d.lines) == 0 {
		return nil
	}
	eol := "\n"
	if d.crlf {
		eol = "\r\n"
	}
	return []byte(strings.Join(d.lines, eol) + eol)
}

// decodeEnvValue implements the env_file value forms: bare (inline " #" comment),
// 'literal' and "double quoted" with the usual escapes.
func decodeEnvValue(raw string) string {
	raw = strings.TrimLeft(raw, " \t")
	switch {
	case strings.HasPrefix(raw, "'"):
		if end := strings.Index(raw[1:], "'"); end >= 0 {
			return raw[1 : 1+end]
		}
		return raw[1:]
	case strings.HasPrefix(raw, `"`):
		var b strings.Builder
		for i := 1; i < len(raw); i++ {
			c := raw[i]
			if c == '"' {
				return b.String()
			}
			if c == '\\' && i+1 < len(raw) {
				i++
				switch raw[i] {
				case 'n':
					b.WriteByte('\n')
				case 'r':
					b.WriteByte('\r')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteByte(raw[i])
				}
				continue
			}
			b.WriteByte(c)
		}
		return b.String()
	}
	if i := strings.Index(raw, " #"); i >= 0 {
		raw = raw[:i]
	} else if i = strings.Index(raw, "\t#"); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimRight(raw, " \t")
}

// encodeEnvValue picks the safest representation that Compose reads back
// unchanged. Single quotes are literal (no interpolation or escapes).
func encodeEnvValue(v string) (string, error) {
	if strings.ContainsAny(v, "\n\r\x00") {
		return "", fmt.Errorf("environment values cannot contain newlines or NUL")
	}
	switch {
	case v == "":
		return "", nil
	case envBare.MatchString(v):
		return v, nil
	case !strings.Contains(v, "'"):
		return "'" + v + "'", nil
	case strings.Contains(v, "$"):
		return "", fmt.Errorf("a value containing both ' and $ cannot be stored safely")
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`, nil
}

// variables lists valid (managed) names in file order; the last assignment wins.
func (d *envDoc) variables() []domain.EnvPair {
	index := map[string]int{}
	out := []domain.EnvPair{}
	for _, line := range d.lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		m := envAssign.FindStringSubmatch(line)
		if m == nil || !domain.ValidEnvName(m[2]) {
			continue
		}
		pair := domain.EnvPair{Name: m[2], Value: decodeEnvValue(m[4])}
		if i, ok := index[m[2]]; ok {
			out[i] = pair
			continue
		}
		index[m[2]] = len(out)
		out = append(out, pair)
	}
	return out
}

func (d *envDoc) lookup(name string) (string, bool) {
	for _, v := range d.variables() {
		if v.Name == name {
			return v.Value, true
		}
	}
	return "", false
}

func (d *envDoc) positions(name string) []int {
	var out []int
	for i, line := range d.lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if m := envAssign.FindStringSubmatch(line); m != nil && m[2] == name {
			out = append(out, i)
		}
	}
	return out
}

func (d *envDoc) set(name, value string) error {
	encoded, err := encodeEnvValue(value)
	if err != nil {
		return err
	}
	pos := d.positions(name)
	if len(pos) == 0 {
		d.lines = append(d.lines, name+"="+encoded)
		return nil
	}
	last := pos[len(pos)-1]
	m := envAssign.FindStringSubmatch(d.lines[last])
	d.lines[last] = m[1] + name + "=" + encoded
	d.remove(pos[:len(pos)-1])
	return nil
}
func (d *envDoc) delete(name string) { d.remove(d.positions(name)) }
func (d *envDoc) remove(pos []int) {
	if len(pos) == 0 {
		return
	}
	drop := map[int]bool{}
	for _, p := range pos {
		drop[p] = true
	}
	kept := d.lines[:0:0]
	for i, line := range d.lines {
		if !drop[i] {
			kept = append(kept, line)
		}
	}
	d.lines = kept
}

// apply performs reviewed changes of one scope's file.
func (d *envDoc) apply(changes []domain.EnvChange) error {
	for _, c := range changes {
		if err := c.Validate(); err != nil {
			return err
		}
		if c.Action == "delete" {
			d.delete(c.Name)
		} else if err := d.set(c.Name, c.Value); err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
	}
	return nil
}
