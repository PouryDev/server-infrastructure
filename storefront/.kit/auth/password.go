package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// PasswordHasher is what use cases depend on; Argon2id is the implementation.
type PasswordHasher interface {
	Hash(password string) (string, error)
	// Verify reports whether password matches the encoded hash, and whether the hash was made with
	// weaker parameters than the current ones and should be replaced after a successful login.
	Verify(password, encoded string) (ok, needsRehash bool, err error)
}

// Argon2id hashes passwords with argon2id and stores them in the standard PHC string format
// ($argon2id$v=19$m=...,t=...,p=...$salt$hash), so parameters can be raised later without a migration.
type Argon2id struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltLen     uint32
	KeyLen      uint32
}

var _ PasswordHasher = (*Argon2id)(nil)

// NewArgon2id returns the OWASP-recommended minimum (19 MiB, 2 iterations, 1 lane). Raise MemoryKiB if
// the container memory limit allows: each concurrent login needs that much memory.
func NewArgon2id() *Argon2id {
	return &Argon2id{MemoryKiB: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLen: 16, KeyLen: 32}
}

func (a *Argon2id) Hash(password string) (string, error) {
	salt := make([]byte, a.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, a.Iterations, a.MemoryKiB, a.Parallelism, a.KeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, a.MemoryKiB, a.Iterations, a.Parallelism, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

func (a *Argon2id) Verify(password, encoded string) (bool, bool, error) {
	p, salt, want, err := parseArgon2(encoded)
	if err != nil {
		return false, false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.Iterations, p.MemoryKiB, p.Parallelism, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false, nil
	}
	weaker := p.MemoryKiB < a.MemoryKiB || p.Iterations < a.Iterations || p.Parallelism < a.Parallelism
	return true, weaker, nil
}

// Upper bounds protect against a tampered hash forcing a huge allocation.
const (
	maxMemoryKiB   = 1 << 20 // 1 GiB
	maxIterations  = 20
	maxParallelism = 32
)

func parseArgon2(encoded string) (p Argon2id, salt, key []byte, err error) {
	bad := errors.New("auth: malformed argon2id hash")
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return p, nil, nil, bad
	}
	var version int
	if _, err = fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return p, nil, nil, bad
	}
	var par uint32
	if _, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.MemoryKiB, &p.Iterations, &par); err != nil {
		return p, nil, nil, bad
	}
	if p.MemoryKiB == 0 || p.MemoryKiB > maxMemoryKiB || p.Iterations == 0 || p.Iterations > maxIterations ||
		par == 0 || par > maxParallelism {
		return p, nil, nil, bad
	}
	p.Parallelism = uint8(par)
	b64 := base64.RawStdEncoding
	if salt, err = b64.DecodeString(parts[4]); err != nil || len(salt) == 0 {
		return p, nil, nil, bad
	}
	if key, err = b64.DecodeString(parts[5]); err != nil || len(key) == 0 {
		return p, nil, nil, bad
	}
	return p, salt, key, nil
}
