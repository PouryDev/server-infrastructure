package smtp

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/notify"
	"github.com/PouryDev/server-infrastructure/gokit/retry"
)

// fakeServer is a minimal plaintext SMTP server. rcptReply lets a test make RCPT fail.
type fakeServer struct {
	ln        net.Listener
	rcptReply string
	mailReply string
	got       chan captured
}

type captured struct {
	from  string
	rcpts []string
	data  string
}

func newFake(t *testing.T) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeServer{ln: ln, got: make(chan captured, 4)}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeServer) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	r := bufio.NewReader(c)
	say := func(s string) { _, _ = fmt.Fprintf(c, "%s\r\n", s) }
	say("220 fake ESMTP")
	var cap captured
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250 fake")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			if f.mailReply != "" {
				say(f.mailReply)
				continue
			}
			cap.from = between(line)
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			if f.rcptReply != "" {
				say(f.rcptReply)
				continue
			}
			cap.rcpts = append(cap.rcpts, between(line))
			say("250 ok")
		case cmd == "DATA":
			say("354 go ahead")
			var sb strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				sb.WriteString(l)
			}
			cap.data = sb.String()
			f.got <- cap
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("502 unknown")
		}
	}
}

func between(line string) string {
	i, j := strings.IndexByte(line, '<'), strings.IndexByte(line, '>')
	if i < 0 || j < i {
		return ""
	}
	return line[i+1 : j]
}

func (f *fakeServer) sender(t *testing.T) *Sender {
	t.Helper()
	host, port, _ := net.SplitHostPort(f.ln.Addr().String())
	p, _ := strconv.Atoi(port)
	s, err := New(Config{Host: host, Port: p, TLS: NoTLS, From: "App <noreply@example.com>"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestSendDeliversEnvelopeAndMIME(t *testing.T) {
	f := newFake(t)
	err := f.sender(t).SendEmail(ctx(t), notify.Email{
		ID: "n1", To: []string{"ada@example.com"}, Cc: []string{"cc@example.com"}, Bcc: []string{"secret@example.com"},
		Subject: "سلام دنیا", Text: "متن ساده", HTML: "<b>متن</b>", Headers: map[string]string{"list-unsubscribe": "<mailto:u@example.com>"},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := <-f.got
	if c.from != "noreply@example.com" {
		t.Errorf("envelope from = %q", c.from)
	}
	if len(c.rcpts) != 3 {
		t.Errorf("rcpts = %v (Bcc must be delivered to)", c.rcpts)
	}
	if strings.Contains(c.data, "secret@example.com") {
		t.Error("Bcc leaked into the message headers")
	}
	for _, want := range []string{"Subject: =?utf-8?q?", "multipart/alternative", "text/plain", "text/html", "List-Unsubscribe:", "Message-ID: <n1."} {
		if !strings.Contains(c.data, want) {
			t.Errorf("message lacks %q:\n%s", want, c.data)
		}
	}
	if strings.Contains(c.data, "\n") && strings.Contains(strings.ReplaceAll(c.data, "\r\n", ""), "\n") {
		t.Error("bare LF in message")
	}
}

func TestClassification(t *testing.T) {
	cases := []struct {
		name      string
		rcpt      string
		permanent bool
	}{
		{"unknown mailbox is permanent", "550 no such user", true},
		{"greylisting is transient", "451 try again later", false},
		{"mailbox full is transient", "452 insufficient storage", false},
	}
	for _, tc := range cases {
		f := newFake(t)
		f.rcptReply = tc.rcpt
		err := f.sender(t).SendEmail(ctx(t), notify.Email{To: []string{"a@example.com"}, Subject: "s", Text: "t"})
		if err == nil {
			t.Fatalf("%s: want error", tc.name)
		}
		if retry.IsPermanent(err) != tc.permanent {
			t.Errorf("%s: permanent=%v, got %v", tc.name, retry.IsPermanent(err), err)
		}
	}
}

func TestConnectionRefusedIsTransient(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().(*net.TCPAddr)
	_ = ln.Close()
	s, _ := New(Config{Host: "127.0.0.1", Port: addr.Port, TLS: NoTLS, From: "a@example.com"})
	err := s.SendEmail(ctx(t), notify.Email{To: []string{"a@example.com"}, Subject: "s", Text: "t"})
	if err == nil || retry.IsPermanent(err) {
		t.Fatalf("want a transient error, got %v", err)
	}
}

func TestContextCancelStopsHungServer(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { _ = ln.Close() })
	go func() { // accepts and never speaks
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(port)
	s, _ := New(Config{Host: host, Port: p, TLS: NoTLS, From: "a@example.com"})
	c, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := s.SendEmail(c, notify.Email{To: []string{"a@example.com"}, Subject: "s", Text: "t"}); err == nil {
		t.Fatal("want an error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("the context deadline was not honoured")
	}
}

func TestStartTLSIsRequired(t *testing.T) {
	f := newFake(t) // does not offer STARTTLS
	host, port, _ := net.SplitHostPort(f.ln.Addr().String())
	p, _ := strconv.Atoi(port)
	s, _ := New(Config{Host: host, Port: p, TLS: StartTLS, From: "a@example.com", Username: "u", Password: "p"})
	err := s.SendEmail(ctx(t), notify.Email{To: []string{"a@example.com"}, Subject: "s", Text: "t"})
	if err == nil || !retry.IsPermanent(err) {
		t.Fatalf("a server without STARTTLS must be refused permanently, got %v", err)
	}
}

func TestNewValidates(t *testing.T) {
	for name, cfg := range map[string]Config{
		"no host":  {From: "a@example.com"},
		"bad from": {Host: "h", From: "nope"},
		"bad tls":  {Host: "h", From: "a@example.com", TLS: "weird"},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	s, _ := New(Config{Host: "h", From: "a@example.com", TLS: ImplicitTLS})
	if s.addr != "h:465" {
		t.Errorf("default port: %s", s.addr)
	}
}

func BenchmarkBuildMessage(b *testing.B) {
	e := notify.Email{ID: "n", To: []string{"a@example.com"}, Subject: "Your code", Text: strings.Repeat("hello ", 50), HTML: strings.Repeat("<p>hello</p>", 50)}
	now := time.Now()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = buildMessage("noreply@example.com", e, now)
	}
}
