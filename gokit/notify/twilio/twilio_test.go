package twilio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PouryDev/server-infrastructure/gokit/notify"
	"github.com/PouryDev/server-infrastructure/gokit/retry"
)

func newSender(t *testing.T, from string, status int, body string, form *map[string]string) *Sender {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if user, pass, ok := r.BasicAuth(); !ok || user != "AC1" || pass != "tok" {
			t.Errorf("basic auth missing or wrong")
		}
		if form != nil {
			*form = map[string]string{"path": r.URL.Path}
			for k := range r.PostForm {
				(*form)[k] = r.PostForm.Get(k)
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	s, err := New(Config{AccountSID: "AC1", AuthToken: "tok", From: from, BaseURL: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSend(t *testing.T) {
	var form map[string]string
	s := newSender(t, "+15005550006", 201, `{"sid":"SM1"}`, &form)
	if err := s.SendSMS(context.Background(), notify.SMS{To: "+989123456789", Body: "hi"}); err != nil {
		t.Fatal(err)
	}
	if form["path"] != "/2010-04-01/Accounts/AC1/Messages.json" || form["To"] != "+989123456789" || form["From"] != "+15005550006" || form["Body"] != "hi" {
		t.Errorf("%v", form)
	}
}

func TestMessagingService(t *testing.T) {
	var form map[string]string
	s := newSender(t, "MG123", 201, `{}`, &form)
	_ = s.SendSMS(context.Background(), notify.SMS{To: "+989123456789", Body: "hi"})
	if form["MessagingServiceSid"] != "MG123" || form["From"] != "" {
		t.Errorf("%v", form)
	}
}

func TestClassification(t *testing.T) {
	for _, tc := range []struct {
		status    int
		permanent bool
	}{{400, true}, {401, true}, {429, false}, {503, false}} {
		s := newSender(t, "+1", tc.status, `{"code":21211,"message":"invalid"}`, nil)
		err := s.SendSMS(context.Background(), notify.SMS{To: "+989123456789", Body: "x"})
		if err == nil || retry.IsPermanent(err) != tc.permanent {
			t.Errorf("status %d: %v", tc.status, err)
		}
	}
}

func TestLocalNumberIsRejected(t *testing.T) {
	s := newSender(t, "+1", 201, `{}`, nil)
	if err := s.SendSMS(context.Background(), notify.SMS{To: "09123456789", Body: "x"}); err == nil || !retry.IsPermanent(err) {
		t.Fatalf("got %v", err)
	}
}
