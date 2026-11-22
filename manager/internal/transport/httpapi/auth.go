package httpapi

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type session struct {
	User, CSRF string
	Expires    time.Time
}
type attempt struct {
	Count int
	Until time.Time
}
type Auth struct {
	mu         sync.Mutex
	username   string
	salt, hash []byte
	sessions   map[string]session
	attempts   map[string]attempt
	secure     bool
	loginSlots chan struct{}
}

func NewAuth(username, passwordHash string, secure bool) (*Auth, error) {
	parts := strings.Split(passwordHash, ":")
	if username == "" || len(parts) != 2 {
		return nil, fmt.Errorf("manager credentials are not configured")
	}
	salt, e1 := hex.DecodeString(parts[0])
	hash, e2 := hex.DecodeString(parts[1])
	if e1 != nil || e2 != nil || len(salt) != 16 || len(hash) != 32 {
		return nil, fmt.Errorf("invalid manager password hash")
	}
	return &Auth{username: username, salt: salt, hash: hash, sessions: map[string]session{}, attempts: map[string]attempt{}, secure: secure, loginSlots: make(chan struct{}, 2)}, nil
}

func randomToken() string { return rand.Text() }
func (a *Auth) cookie(value string, age int) *http.Cookie {
	return &http.Cookie{Name: "manager_session", Value: value, Path: "/", MaxAge: age, HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode}
}

func (a *Auth) login(w http.ResponseWriter, r *http.Request, username, password string) (session, int) {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	// Deliberately use the direct peer: forwarded headers must not bypass limits.
	// nginx applies a separate per-client limit before this backend-wide limit.
	a.mu.Lock()
	now := time.Now()
	for key, v := range a.attempts {
		if now.After(v.Until) {
			delete(a.attempts, key)
		}
	}
	for key, v := range a.sessions {
		if now.After(v.Expires) {
			delete(a.sessions, key)
		}
	}
	v := a.attempts[ip]
	if v.Count >= 5 || len(a.attempts) > 1024 || len(a.sessions) > 128 {
		a.mu.Unlock()
		return session{}, 429
	}
	v.Count++
	if v.Until.IsZero() {
		v.Until = now.Add(5 * time.Minute)
	}
	a.attempts[ip] = v
	a.mu.Unlock()
	select {
	case a.loginSlots <- struct{}{}:
		defer func() { <-a.loginSlots }()
	default:
		return session{}, 429
	}
	key, err := pbkdf2.Key(sha256.New, password, a.salt, 600000, 32)
	if err != nil || subtle.ConstantTimeCompare(key, a.hash) != 1 || subtle.ConstantTimeCompare([]byte(username), []byte(a.username)) != 1 {
		return session{}, 401
	}
	s := session{User: a.username, CSRF: randomToken(), Expires: now.Add(8 * time.Hour)}
	token := randomToken()
	digest := sha256.Sum256([]byte(token))
	a.mu.Lock()
	delete(a.attempts, ip)
	a.sessions[hex.EncodeToString(digest[:])] = s
	a.mu.Unlock()
	http.SetCookie(w, a.cookie(token, 8*60*60))
	return s, 200
}

func (a *Auth) current(r *http.Request) (session, bool) {
	c, err := r.Cookie("manager_session")
	if err != nil {
		return session{}, false
	}
	digest := sha256.Sum256([]byte(c.Value))
	key := hex.EncodeToString(digest[:])
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[key]
	if ok && time.Now().After(s.Expires) {
		delete(a.sessions, key)
		return session{}, false
	}
	return s, ok
}
func (a *Auth) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("manager_session"); err == nil {
		digest := sha256.Sum256([]byte(c.Value))
		a.mu.Lock()
		delete(a.sessions, hex.EncodeToString(digest[:]))
		a.mu.Unlock()
	}
	http.SetCookie(w, a.cookie("", -1))
}
