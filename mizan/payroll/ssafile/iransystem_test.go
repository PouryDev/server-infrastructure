package ssafile

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// The vectors are the generic job titles, sex and nationality values of the owner's real sample DSKWOR00.dbf (no personal data): the stored bytes were decoded with the Pournader
// table and re-encoded here; the encoder must reproduce the real bytes exactly.
var realVectors = []struct{ hex, text string }{
	{"a2a4f5", "مرد"},
	{"f6a5", "زن"},
	{"fcf790a4fe90", "ایرانی"},
	{"a2f7f5a491ee", "کارمند"},
	{"f9a4fea2f520968efefb20a7fe8ea4", "رئیس هیئت مدیره"},
	{"f1f591e4a4fea2f5", "مدیرعامل"},
	{"f9a291a820a4f0a491ee", "کارگر ساده"},
	{"fcf7ea20f1f88ea8f5", "مسئول فنی"},
}

func TestIranSystemReproducesRealSampleBytes(t *testing.T) {
	var enc IranSystem
	for _, v := range realVectors {
		want, _ := hex.DecodeString(v.hex)
		got, err := enc.Encode(v.text)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%q: got %x err %v, want %x", v.text, got, err, want)
		}
		back, err := enc.Decode(want)
		if err != nil || back != v.text {
			t.Errorf("%x decodes to %q (%v), want %q", want, back, err, v.text)
		}
	}
}

func TestIranSystemShapingAndOrder(t *testing.T) {
	var enc IranSystem
	for _, c := range []struct{ text, hex string }{{"سلام", "f4f2a8"}, {"خانه", "f9f791a1"}} {
		got, err := enc.Encode(c.text)
		if err != nil || hex.EncodeToString(got) != c.hex {
			t.Errorf("%q: %x %v", c.text, got, err)
		}
	}
	// A ZWNJ breaks the joining: the heh before it is final, the next heh initial; the stored order is reversed.
	got, err := enc.Encode("خانه\u200cها")
	if err != nil || hex.EncodeToString(got) != "91fb"+"f9f791a1" {
		t.Errorf("zwnj word: %x %v", got, err)
	}
	// Digits and Latin text keep their left-to-right order inside the reversed line; ASCII digits stay ASCII, Persian digits use 0x80-0x89.
	got, err = enc.Encode("شماره 123")
	if err != nil || string(got[:3]) != "123" {
		t.Errorf("digits: %x %v", got, err)
	}
	got, err = enc.Encode("۱۲۳")
	if err != nil || !bytes.Equal(got, []byte{0x81, 0x82, 0x83}) {
		t.Errorf("persian digits: %x %v", got, err)
	}
	// Arabic yeh and kaf are normalised.
	a, _ := enc.Encode("علي")
	b, _ := enc.Encode("علی")
	if !bytes.Equal(a, b) {
		t.Error("arabic yeh not normalised")
	}
	// Fail closed on what the table cannot show.
	for _, bad := range []string{"ۀ", "ة", "گچە", "日本", "نئ"} {
		if _, err := enc.Encode(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	for _, s := range []string{"a\nb"} {
		if _, err := enc.Encode(s); err == nil {
			t.Errorf("%q must be refused", s)
		}
	}
	// Round trips of ordinary text.
	for _, s := range []string{"علی رضایی", "شرکت (سهامی خاص) 12", "کد 123 ملی", "تهران، خیابان آزادی"} {
		enc1, err := enc.Encode(s)
		if err != nil {
			t.Errorf("%q: %v", s, err)
			continue
		}
		if back, err := enc.Decode(enc1); err != nil || back != s {
			t.Errorf("round trip %q -> %q (%v)", s, back, err)
		}
	}
}
