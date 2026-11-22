package smtp

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"sort"
	"strings"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/notify"
)

// buildMessage renders e as an RFC 5322 message with CRLF line endings. Bcc recipients are deliberately
// absent from the headers.
func buildMessage(from string, e notify.Email, now time.Time) ([]byte, error) {
	var b bytes.Buffer
	h := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }

	h("From", from)
	if len(e.To) > 0 {
		h("To", strings.Join(e.To, ", "))
	}
	if len(e.Cc) > 0 {
		h("Cc", strings.Join(e.Cc, ", "))
	}
	if e.ReplyTo != "" {
		h("Reply-To", e.ReplyTo)
	}
	h("Subject", mime.QEncoding.Encode("utf-8", e.Subject))
	h("Date", now.Format(time.RFC1123Z))
	h("Message-ID", messageID(e.ID, from))
	h("MIME-Version", "1.0")
	keys := make([]string, 0, len(e.Headers))
	for k := range e.Headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h(textproto.CanonicalMIMEHeaderKey(k), mime.QEncoding.Encode("utf-8", e.Headers[k]))
	}

	switch {
	case e.Text != "" && e.HTML != "":
		mw := multipart.NewWriter(&b)
		h("Content-Type", `multipart/alternative; boundary="`+mw.Boundary()+`"`)
		b.WriteString("\r\n")
		for _, part := range []struct{ ctype, body string }{
			{"text/plain", e.Text}, {"text/html", e.HTML}, // the last alternative is the preferred one
		} {
			pw, err := mw.CreatePart(textproto.MIMEHeader{
				"Content-Type":              {part.ctype + `; charset="utf-8"`},
				"Content-Transfer-Encoding": {"quoted-printable"},
			})
			if err != nil {
				return nil, err
			}
			if err := writeQP(pw, part.body); err != nil {
				return nil, err
			}
		}
		if err := mw.Close(); err != nil {
			return nil, err
		}
	default:
		ctype, body := "text/plain", e.Text
		if e.HTML != "" {
			ctype, body = "text/html", e.HTML
		}
		h("Content-Type", ctype+`; charset="utf-8"`)
		h("Content-Transfer-Encoding", "quoted-printable")
		b.WriteString("\r\n")
		if err := writeQP(&b, body); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}

func writeQP(w interface{ Write([]byte) (int, error) }, body string) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := qp.Write([]byte(body)); err != nil {
		return err
	}
	return qp.Close()
}

// messageID builds a globally unique Message-ID in the sender's domain; the notification id is part of it
// so a bounce can be traced back to the log line.
func messageID(id, from string) string {
	domain := "localhost"
	if p, err := mail.ParseAddress(from); err == nil {
		if at := strings.LastIndexByte(p.Address, '@'); at >= 0 {
			domain = p.Address[at+1:]
		}
	}
	var r [8]byte
	_, _ = rand.Read(r[:])
	if id == "" {
		id = "n"
	}
	return "<" + id + "." + hex.EncodeToString(r[:]) + "@" + domain + ">"
}
