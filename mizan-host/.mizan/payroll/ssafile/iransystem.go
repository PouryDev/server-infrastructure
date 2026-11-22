//nolint:staticcheck // the Persian text uses the zero-width non-joiner (U+200C), which is correct orthography
package ssafile

import (
	"fmt"
	"strings"
)

// IranSystem is the TextEncoder for the Iran System code page (ایران سیستم), the encoding of the Persian text in the Social Security list files.
//
// SOURCES: the code page table is Roozbeh Pournader's "IRAN SYSTEM to Unicode" v1.0 (2000; archived copy of http://sofia.nmsu.edu/~mleisher/Software/csets/IRANSYSTEM.TXT), checked byte
// for byte against the table of Wikipedia (en, "Iran System encoding") and the independent python-iransystem 0.5.1 (identical on all 128 upper bytes and on the positional form of all 35
// letters). The visual-order rule (each line is stored as it appears on screen: the Persian letters are reversed, runs of digits and Latin text keep their left-to-right order, brackets
// are mirrored) is the one python-iransystem documents; its author calls it a heuristic made to match real files. VERIFIED against the owner's real sample DSKWOR00.dbf: every distinct job
// title, sex and nationality value of the sample decodes to the right word and re-encodes to exactly the same bytes (see the test vectors, which hold only those generic words).
// NOT verified: names with characters the sample does not contain (ئ at the end of a word and ء/ۀ/ة are refused rather than guessed), mixed Latin text, brackets.
type IranSystem struct{}

var _ TextEncoder = IranSystem{}

// letter holds the bytes of a Persian letter by position; 0 means the form does not exist in the code page.
type letter struct {
	iso, fin, ini, med byte
	joinsNext          bool // dual-joining: connects to the following letter
}

func one(b byte) letter        { return letter{b, b, 0, 0, false} }
func two(fin, ini byte) letter { return letter{fin, fin, ini, ini, true} }

var letters = map[rune]letter{
	'آ': one(0x8D), 'ء': one(0x8F), 'ا': {0x90, 0x91, 0, 0, false},
	'ب': two(0x92, 0x93), 'پ': two(0x94, 0x95), 'ت': two(0x96, 0x97), 'ث': two(0x98, 0x99), 'ج': two(0x9A, 0x9B), 'چ': two(0x9C, 0x9D),
	'ح': two(0x9E, 0x9F), 'خ': two(0xA0, 0xA1), 'د': one(0xA2), 'ذ': one(0xA3), 'ر': one(0xA4), 'ز': one(0xA5), 'ژ': one(0xA6),
	'س': two(0xA7, 0xA8), 'ش': two(0xA9, 0xAA), 'ص': two(0xAB, 0xAC), 'ض': two(0xAD, 0xAE),
	'ط': {0xAF, 0xAF, 0xAF, 0xAF, true}, 'ظ': {0xE0, 0xE0, 0xE0, 0xE0, true},
	'ع': {0xE1, 0xE2, 0xE4, 0xE3, true}, 'غ': {0xE5, 0xE6, 0xE8, 0xE7, true},
	'ف': two(0xE9, 0xEA), 'ق': two(0xEB, 0xEC), 'ک': two(0xED, 0xEE), 'گ': two(0xEF, 0xF0), 'ل': two(0xF1, 0xF3), 'م': two(0xF4, 0xF5), 'ن': two(0xF6, 0xF7),
	'و': one(0xF8), 'ه': {0xF9, 0xF9, 0xFB, 0xFA, true}, 'ی': {0xFD, 0xFC, 0xFE, 0xFE, true},
	// Hamza on yeh exists only as a joining-before form (0x8E): at the end of a word it is refused.
	'ئ': {0, 0, 0x8E, 0x8E, true},
}

const lamAlef = 0xF2

// arabic variants of Persian letters are accepted and normalised.
var variants = map[rune]rune{'ي': 'ی', 'ى': 'ی', 'ك': 'ک'}

type elem struct {
	b   byte
	ltr bool // part of a left-to-right run (Latin letters, digits and their inner joiners)
}

func isLatinAlnum(c rune) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

const innerJoiners = ".,:'_-/@+%#$&=*\\~"

var mirror = map[byte]byte{'(': ')', ')': '(', '[': ']', ']': '[', '{': '}', '}': '{', '<': '>', '>': '<'}

// Encode implements TextEncoder: logical Persian text in, Iran System bytes in visual order out.
func (IranSystem) Encode(s string) ([]byte, error) {
	if strings.ContainsAny(s, "\r\n") {
		return nil, fmt.Errorf("ssafile: iran system text is one line")
	}
	// A zero-width non-joiner only breaks the joining: shape the parts on both sides of it separately.
	var el []elem
	for _, part := range strings.Split(s, "\u200c") {
		sub, err := shape(part)
		if err != nil {
			return nil, err
		}
		el = append(el, sub...)
	}
	return visual(el), nil
}

// shape turns text without ZWNJ into elements in logical order, choosing the positional form of every letter.
func shape(s string) ([]elem, error) {
	rs := []rune(s)
	for i, r := range rs {
		if v, ok := variants[r]; ok {
			rs[i] = v
		}
	}
	var el []elem
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		l, isLetter := letters[r]
		if !isLetter {
			e, err := plain(r)
			if err != nil {
				return nil, err
			}
			el = append(el, e)
			continue
		}
		prevJoins := false
		if i > 0 {
			if pl, ok := letters[rs[i-1]]; ok && pl.joinsNext {
				prevJoins = true
			}
		}
		nextJoins := false
		if l.joinsNext && i+1 < len(rs) {
			if _, ok := letters[rs[i+1]]; ok && rs[i+1] != 'ء' {
				nextJoins = true
			}
		}
		if r == 'ل' && i+1 < len(rs) && rs[i+1] == 'ا' {
			el = append(el, elem{b: lamAlef})
			i++
			continue
		}
		var b byte
		switch {
		case prevJoins && nextJoins:
			b = l.med
		case nextJoins:
			b = l.ini
		case prevJoins:
			b = l.fin
		default:
			b = l.iso
		}
		if b == 0 {
			return nil, fmt.Errorf("ssafile: iran system has no form of %q in this position (refused, not guessed)", string(r))
		}
		el = append(el, elem{b: b})
	}
	return el, nil
}

func plain(r rune) (elem, error) {
	switch {
	case r >= '۰' && r <= '۹':
		return elem{b: byte(0x80 + r - '۰'), ltr: true}, nil
	case r >= '٠' && r <= '٩':
		return elem{b: byte(0x80 + r - '٠'), ltr: true}, nil
	case r == '،':
		return elem{b: 0x8A}, nil
	case r == 'ـ':
		return elem{b: 0x8B}, nil
	case r == '؟':
		return elem{b: 0x8C}, nil
	case r < 0x80:
		return elem{b: byte(r), ltr: isLatinAlnum(r)}, nil
	}
	return elem{}, fmt.Errorf("ssafile: character %q (U+%04X) is not in the Iran System code page", string(r), r)
}

// visual puts logical-order elements into storage order: the line is reversed, left-to-right runs (digits, Latin text, with inner joiners) keep their own order, brackets are mirrored.
func visual(el []elem) []byte {
	// absorb inner joiners between two ltr elements
	for i := 1; i+1 < len(el); i++ {
		if !el[i].ltr && el[i].b < 0x80 && strings.IndexByte(innerJoiners, el[i].b) >= 0 && el[i-1].ltr && el[i+1].ltr {
			el[i].ltr = true
		}
	}
	type run struct {
		b     []byte
		latin bool
		ltr   bool
	}
	var runs []run
	for _, e := range el {
		if e.ltr {
			if n := len(runs); n > 0 && runs[n-1].ltr {
				runs[n-1].b = append(runs[n-1].b, e.b)
				runs[n-1].latin = runs[n-1].latin || (e.b < 0x80 && !(e.b >= '0' && e.b <= '9') && strings.IndexByte(innerJoiners, e.b) < 0)
				continue
			}
			runs = append(runs, run{b: []byte{e.b}, ltr: true, latin: e.b < 0x80 && !(e.b >= '0' && e.b <= '9') && strings.IndexByte(innerJoiners, e.b) < 0})
			continue
		}
		b := e.b
		if m, ok := mirror[b]; ok {
			b = m
		}
		runs = append(runs, run{b: []byte{b}})
	}
	// chains separated only by spaces form one run when one of them has a Latin letter
	var merged []run
	for i := 0; i < len(runs); i++ {
		r := runs[i]
		if r.ltr {
			j, latin, buf := i, r.latin, append([]byte(nil), r.b...)
			for k := i + 1; k+1 < len(runs); k += 2 {
				if len(runs[k].b) == 1 && runs[k].b[0] == ' ' && !runs[k].ltr && runs[k+1].ltr {
					latin = latin || runs[k+1].latin
					buf = append(append(buf, ' '), runs[k+1].b...)
					j = k + 1
					continue
				}
				break
			}
			if latin && j > i {
				merged = append(merged, run{b: buf, ltr: true, latin: true})
				i = j
				continue
			}
		}
		merged = append(merged, r)
	}
	var out []byte
	for i := len(merged) - 1; i >= 0; i-- {
		out = append(out, merged[i].b...)
	}
	return out
}

// Decode is the inverse of Encode (logical Unicode text from stored bytes). The ZWNJ is not restored. It exists for tests and diagnostics.
func (IranSystem) Decode(b []byte) (string, error) {
	text := make([]rune, 0, len(b))
	isLtr := make([]bool, 0, len(b))
	for _, c := range b {
		switch {
		case c < 0x80:
			text = append(text, rune(c))
			isLtr = append(isLtr, isLatinAlnum(rune(c)))
		case c >= 0x80 && c <= 0x89:
			text = append(text, '۰'+rune(c-0x80))
			isLtr = append(isLtr, true)
		case c == 0x8A:
			text, isLtr = append(text, '،'), append(isLtr, false)
		case c == 0x8B:
			text, isLtr = append(text, 'ـ'), append(isLtr, false)
		case c == 0x8C:
			text, isLtr = append(text, '؟'), append(isLtr, false)
		case c == lamAlef:
			text, isLtr = append(text, 'ل', 'ا'), append(isLtr, false, false)
		default:
			r, ok := byteLetter[c]
			if !ok {
				return "", fmt.Errorf("ssafile: byte 0x%02X is not a Persian letter of the Iran System code page", c)
			}
			text, isLtr = append(text, r), append(isLtr, false)
		}
	}
	// reverse the line, then put the left-to-right runs back
	n := len(text)
	rt := make([]rune, n)
	rl := make([]bool, n)
	for i := range text {
		rt[n-1-i], rl[n-1-i] = text[i], isLtr[i]
	}
	for i := 1; i+1 < n; i++ {
		if !rl[i] && rt[i] < 0x80 && strings.ContainsRune(innerJoiners, rt[i]) && rl[i-1] && rl[i+1] {
			rl[i] = true
		}
	}
	var out []rune
	for i := 0; i < n; {
		if rl[i] {
			j := i
			for j < n && rl[j] {
				j++
			}
			for k := j - 1; k >= i; k-- {
				out = append(out, rt[k])
			}
			i = j
			continue
		}
		r := rt[i]
		if r < 0x80 {
			if m, ok := mirror[byte(r)]; ok {
				r = rune(m)
			}
		}
		out = append(out, r)
		i++
	}
	return string(out), nil
}

var byteLetter = func() map[byte]rune {
	m := map[byte]rune{}
	for r, l := range letters {
		for _, b := range []byte{l.iso, l.fin, l.ini, l.med} {
			if b != 0 {
				m[b] = r
			}
		}
	}
	return m
}()
