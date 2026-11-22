// Package ssafile writes the monthly insurance list files of the Social Security Organization (DSKKAR00.DBF, the workshop, and DSKWOR00.DBF, the insured persons) as dBASE III files.
//
// WHAT IS VERIFIED and what is not (see mizan/README.md, "Payroll official files"): the field names, types and widths were read from real sample files of the owner of the system (structure only:
// no value was copied); the file layout is plain dBASE III (version byte 0x03, 32-byte header, 32-byte field descriptors, 0x0D terminator, records with a space delete flag, 0x1A end mark,
// numbers right-aligned and text left-aligned, both space padded). No OFFICIAL description of the files was found (ssa.ir / tamin.ir publish only the list program), so the meaning of a field
// that the samples cannot confirm is not guessed: those fields have no default and must be supplied. The sample files store Persian text in the Iran System code page in VISUAL order; text
// goes through a TextEncoder; the default is IranSystem (iransystem.go: table, shaping and visual order, verified against the real sample words).
package ssafile

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// FieldType is a dBASE field type used by the files.
type FieldType byte

// Field types.
const (
	Char    FieldType = 'C'
	Numeric FieldType = 'N'
)

// Field describes one column.
type Field struct {
	Name string
	Type FieldType
	Len  int
}

// TextEncoder converts text to the bytes stored in a character field.
type TextEncoder interface {
	Encode(s string) ([]byte, error)
}

// ASCIIOnly refuses non-ASCII text (the strict encoder: use it to be sure nothing Persian is written). The default is IranSystem.
type ASCIIOnly struct{}

// Encode implements TextEncoder.
func (ASCIIOnly) Encode(s string) ([]byte, error) {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return nil, errors.New("ssafile: Persian text needs a verified Iran System TextEncoder (none is built in)")
		}
		b = append(b, s[i])
	}
	return b, nil
}

// Row is one record: values by field name; text for Char fields, int64 for Numeric fields.
type Row map[string]any

// Write returns a dBASE III file with the fields and rows. enc encodes text (nil = IranSystem). A value that does not fit its field is an error, never truncated.
func Write(fields []Field, rows []Row, enc TextEncoder, now time.Time) ([]byte, error) {
	if enc == nil {
		enc = IranSystem{}
	}
	if len(fields) == 0 || len(fields) > 128 {
		return nil, errors.New("ssafile: 1 to 128 fields")
	}
	recLen := 1
	for _, f := range fields {
		if len(f.Name) == 0 || len(f.Name) > 10 || f.Len < 1 || f.Len > 254 || (f.Type != Char && f.Type != Numeric) {
			return nil, fmt.Errorf("ssafile: bad field %+v", f)
		}
		recLen += f.Len
	}
	hdrLen := 32 + 32*len(fields) + 1
	out := make([]byte, hdrLen, hdrLen+recLen*len(rows)+1)
	out[0] = 0x03
	out[1], out[2], out[3] = byte(now.Year()-1900), byte(now.Month()), byte(now.Day())
	binary.LittleEndian.PutUint32(out[4:], uint32(len(rows)))
	binary.LittleEndian.PutUint16(out[8:], uint16(hdrLen))
	binary.LittleEndian.PutUint16(out[10:], uint16(recLen))
	out[29] = 0x03 // language driver byte as in the sample files
	for i, f := range fields {
		d := out[32+32*i:]
		copy(d[:11], f.Name)
		d[11] = byte(f.Type)
		d[16] = byte(f.Len)
	}
	out[hdrLen-1] = 0x0D
	for ri, row := range rows {
		rec := make([]byte, recLen)
		rec[0] = ' '
		pos := 1
		for _, f := range fields {
			cell := rec[pos : pos+f.Len]
			for i := range cell {
				cell[i] = ' '
			}
			v, ok := row[f.Name]
			if !ok {
				return nil, fmt.Errorf("ssafile: row %d has no value for %s", ri, f.Name)
			}
			switch f.Type {
			case Numeric:
				n, ok := v.(int64)
				if !ok {
					return nil, fmt.Errorf("ssafile: row %d: %s needs an int64", ri, f.Name)
				}
				s := strconv.FormatInt(n, 10)
				if n < 0 || len(s) > f.Len {
					return nil, fmt.Errorf("ssafile: row %d: %s = %d does not fit %d digits (negative values are refused)", ri, f.Name, n, f.Len)
				}
				copy(cell[f.Len-len(s):], s)
			case Char:
				s, ok := v.(string)
				if !ok {
					return nil, fmt.Errorf("ssafile: row %d: %s needs a string", ri, f.Name)
				}
				b, err := enc.Encode(s)
				if err != nil {
					return nil, fmt.Errorf("row %d field %s: %w", ri, f.Name, err)
				}
				if len(b) > f.Len {
					return nil, fmt.Errorf("ssafile: row %d: %s is %d bytes, the field holds %d", ri, f.Name, len(b), f.Len)
				}
				copy(cell, b)
			}
			pos += f.Len
		}
		out = append(out, rec...)
	}
	return append(out, 0x1A), nil
}
