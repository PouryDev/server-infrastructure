package ssafile

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)

// readDBF is a minimal reader used only to check what was written.
func readDBF(t *testing.T, b []byte) (names []string, lens []int, recs [][]string) {
	t.Helper()
	if b[0] != 0x03 || b[len(b)-1] != 0x1A {
		t.Fatalf("not dBASE III: %x ... %x", b[0], b[len(b)-1])
	}
	n := int(binary.LittleEndian.Uint32(b[4:]))
	hdr, rl := int(binary.LittleEndian.Uint16(b[8:])), int(binary.LittleEndian.Uint16(b[10:]))
	nf := (hdr - 33) / 32
	for i := 0; i < nf; i++ {
		d := b[32+32*i:]
		names = append(names, strings.TrimRight(string(d[:11]), "\x00"))
		lens = append(lens, int(d[16]))
	}
	if b[hdr-1] != 0x0D || len(b) != hdr+n*rl+1 {
		t.Fatalf("layout: hdr %d recs %d len %d", hdr, n, len(b))
	}
	for r := 0; r < n; r++ {
		rec := b[hdr+r*rl : hdr+(r+1)*rl]
		if rec[0] != ' ' {
			t.Fatal("delete flag")
		}
		pos, row := 1, []string{}
		for _, l := range lens {
			row = append(row, string(rec[pos:pos+l]))
			pos += l
		}
		recs = append(recs, row)
	}
	return
}

func TestRecordLengthsMatchTheSampleStructure(t *testing.T) {
	sum := func(fs []Field) int {
		n := 1
		for _, f := range fs {
			n += f.Len
		}
		return n
	}
	if sum(KarFields) != 287 || sum(WorFields) != 417 { // record lengths read from the sample files
		t.Fatalf("kar %d wor %d", sum(KarFields), sum(WorFields))
	}
}

func workers() []Worker {
	return []Worker{
		{ID: "12345678", First: "Ali", Last: "Test", Days: 30, DailyWage: 1000, MonthlyWage: 30000, Benefits: 5000, Insurable: 35000, Total: 36000, EmployeeShare: 2450, NationalID: "0000000001", JobCode: "111111", Sex: "M"},
		{ID: "87654321", First: "Sara", Last: "Test", Days: 20, DailyWage: 1500, MonthlyWage: 30000, Benefits: 0, Insurable: 30000, Total: 30000, EmployeeShare: 2100, NationalID: "0000000002", JobCode: "222222", Sex: "F"},
	}
}

func TestBuildTotalsAreSumsOfWorkers(t *testing.T) {
	w := Workshop{ID: "1234567890", Name: "Test Co", ListNo: "000000000001", PaymentMonth: "001", EmployerShare: 13000, UnemploymentCont: 1950, Rate: 30}
	kar, wor, err := Build(1405, 8, w, workers(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	names, lens, recs := readDBF(t, kar)
	if len(names) != 23 || names[0] != "DSK_ID" || len(recs) != 1 {
		t.Fatalf("kar: %v %d", names, len(recs))
	}
	get := func(name string) string {
		for i, n := range names {
			if n == name {
				return strings.TrimSpace(recs[0][i])
			}
		}
		return "?"
	}
	for k, v := range map[string]string{"DSK_NUM": "2", "DSK_TDD": "50", "DSK_TROOZ": "2500", "DSK_TMAH": "60000", "DSK_TMAZ": "5000", "DSK_TMASH": "65000", "DSK_TTOTL": "66000", "DSK_TBIME": "4550", "DSK_YY": "5", "DSK_MM": "8"} {
		if get(k) != v {
			t.Fatalf("%s = %q, want %s", k, get(k), v)
		}
	}
	if lens[9] != 5 || !strings.HasPrefix(recs[0][9], "    2") { // numbers right-aligned
		t.Fatalf("numeric alignment: %q", recs[0][9])
	}
	_, _, wrecs := readDBF(t, wor)
	if len(wrecs) != 2 || !strings.HasPrefix(wrecs[0][5], "Ali ") { // text left-aligned
		t.Fatalf("workers: %q", wrecs)
	}
}

func TestBuildFailsClosed(t *testing.T) {
	w := Workshop{ID: "1234567890", Name: "Test Co", ListNo: "000000000001", PaymentMonth: "001"}
	bad := workers()
	bad[0].MonthlyWage = 29999
	if _, _, err := Build(1405, 8, w, bad, nil, now); err == nil {
		t.Fatal("MAH = ROOZ x DD must hold")
	}
	bad = workers()
	bad[0].Insurable = 1
	if _, _, err := Build(1405, 8, w, bad, nil, now); err == nil {
		t.Fatal("MASH = MAH + MAZ must hold")
	}
	bad = workers()
	bad[0].NationalID = "123"
	if _, _, err := Build(1405, 8, w, bad, nil, now); err == nil {
		t.Fatal("national id")
	}
	bad = workers()
	bad[0].First = "علی"
	if _, _, err := Build(1405, 8, w, bad, ASCIIOnly{}, now); err == nil || !strings.Contains(err.Error(), "Iran System") {
		t.Fatalf("Persian text through the strict ASCII encoder must be refused: %v", err)
	}
	if _, wor, err := Build(1405, 8, w, bad, nil, now); err != nil || len(wor) == 0 {
		t.Fatalf("Persian text goes through the built-in Iran System encoder: %v", err)
	}
	w2 := w
	w2.Name = strings.Repeat("x", 31)
	if _, _, err := Build(1405, 8, w2, workers(), nil, now); err == nil {
		t.Fatal("text longer than its field must be refused, not truncated")
	}
	if _, _, err := Build(1405, 8, Workshop{}, workers(), nil, now); err == nil {
		t.Fatal("workshop data is required")
	}
	if !bytes.Equal(mustWrite(t), mustWrite(t)) {
		t.Fatal("deterministic output")
	}
}

func mustWrite(t *testing.T) []byte {
	t.Helper()
	b, err := Write([]Field{{"A", Char, 3}, {"B", Numeric, 4}}, []Row{{"A": "x", "B": int64(7)}}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
