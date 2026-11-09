package kavenegar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PouryDev/server-infrastructure/gokit/notify"
	"github.com/PouryDev/server-infrastructure/gokit/retry"
)

func server(t *testing.T, status int, body string, seen *http.Request) *Sender {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			_ = r.ParseForm()
			*seen = *r
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	s, err := New(Config{APIKey: "SECRETKEY", Sender: "10004346", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var msg = notify.SMS{To: "09123456789", Body: "کد شما 1234"}

func TestSendOK(t *testing.T) {
	var seen http.Request
	s := server(t, 200, `{"return":{"status":200,"message":"تایید شد"},"entries":[{"messageid":1}]}`, &seen)
	if err := s.SendSMS(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if seen.URL.Path != "/v1/SECRETKEY/sms/send.json" {
		t.Errorf("path %s", seen.URL.Path)
	}
	if seen.PostForm.Get("receptor") != msg.To || seen.PostForm.Get("message") != msg.Body || seen.PostForm.Get("sender") != "10004346" {
		t.Errorf("form %v", seen.PostForm)
	}
}

func TestClassification(t *testing.T) {
	cases := []struct {
		name      string
		httpCode  int
		body      string
		permanent bool
	}{
		{"invalid receiver", 200, `{"return":{"status":411,"message":"bad receptor"}}`, true},
		{"no credit", 200, `{"return":{"status":418,"message":"credit"}}`, true},
		{"gateway error", 200, `{"return":{"status":500,"message":"oops"}}`, false},
		{"rate limited", 200, `{"return":{"status":429,"message":"slow down"}}`, false},
		{"proxy 502 page", 502, `<html>bad gateway</html>`, false},
		{"unauthorised page", 403, `forbidden`, true},
	}
	for _, tc := range cases {
		err := server(t, tc.httpCode, tc.body, nil).SendSMS(context.Background(), msg)
		if err == nil {
			t.Fatalf("%s: want error", tc.name)
		}
		if retry.IsPermanent(err) != tc.permanent {
			t.Errorf("%s: permanent=%v (%v)", tc.name, retry.IsPermanent(err), err)
		}
	}
}

func TestErrorsNeverContainAPIKey(t *testing.T) {
	s, _ := New(Config{APIKey: "SECRETKEY", BaseURL: "http://127.0.0.1:1"}) // connection refused
	err := s.SendSMS(context.Background(), msg)
	if err == nil || strings.Contains(err.Error(), "SECRETKEY") {
		t.Fatalf("error must exist and must not leak the key: %v", err)
	}
	if retry.IsPermanent(err) {
		t.Error("network errors are transient")
	}
}

func TestNewValidates(t *testing.T) {
	for _, k := range []string{"", "a/b", "a?b"} {
		if _, err := New(Config{APIKey: k}); err == nil {
			t.Errorf("key %q accepted", k)
		}
	}
}
