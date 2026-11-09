package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

const (
	algHS256 = "HS256"
	algEdDSA = "EdDSA"

	minSecretLen = 32
)

// Key is one signing or verification key. Create it with HMACKey, Ed25519Key or Ed25519PublicKey.
type Key struct {
	ID     string
	alg    string
	secret []byte
	priv   ed25519.PrivateKey
	pub    ed25519.PublicKey
}

// CanSign reports whether the key can issue tokens (a public-only key can just verify them).
func (k Key) CanSign() bool { return k.secret != nil || k.priv != nil }

// HMACKey builds an HS256 key from a shared secret of at least 32 bytes.
func HMACKey(id string, secret []byte) (Key, error) {
	if len(secret) < minSecretLen {
		return Key{}, fmt.Errorf("auth: HMAC secret must be at least %d bytes", minSecretLen)
	}
	return Key{ID: id, alg: algHS256, secret: secret}, nil
}

// Ed25519Key builds an EdDSA key that signs and verifies from a PKCS#8 PEM private key.
func Ed25519Key(id string, privatePEM []byte) (Key, error) {
	block, _ := pem.Decode(normalizePEM(privatePEM))
	if block == nil {
		return Key{}, errors.New("auth: private key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return Key{}, fmt.Errorf("auth: parse private key: %w", err)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return Key{}, errors.New("auth: private key is not Ed25519")
	}
	return Key{ID: id, alg: algEdDSA, priv: priv, pub: priv.Public().(ed25519.PublicKey)}, nil
}

// Ed25519PublicKey builds a verify-only EdDSA key from a PKIX PEM public key. Services that only
// check tokens (and never issue them) hold nothing but this.
func Ed25519PublicKey(id string, publicPEM []byte) (Key, error) {
	block, _ := pem.Decode(normalizePEM(publicPEM))
	if block == nil {
		return Key{}, errors.New("auth: public key is not PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return Key{}, fmt.Errorf("auth: parse public key: %w", err)
	}
	pub, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return Key{}, errors.New("auth: public key is not Ed25519")
	}
	return Key{ID: id, alg: algEdDSA, pub: pub}, nil
}

// GenerateEd25519 creates a new key pair as PEM (private PKCS#8, public PKIX).
func GenerateEd25519() (privatePEM, publicPEM []byte, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	pk, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	pb, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}),
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pb}), nil
}

// normalizePEM accepts PEM stored in a single-line environment variable with literal "\n".
func normalizePEM(b []byte) []byte {
	return []byte(strings.ReplaceAll(string(b), `\n`, "\n"))
}

// KeySet holds the keys a service accepts. The first key that can sign issues new tokens; all keys
// verify. To rotate: put the new key first and keep the old one until its tokens have expired.
type KeySet struct {
	signer Key
	byID   map[string]Key
}

// NewKeySet validates and indexes keys.
func NewKeySet(keys ...Key) (*KeySet, error) {
	if len(keys) == 0 {
		return nil, errors.New("auth: at least one key is required")
	}
	ks := &KeySet{byID: make(map[string]Key, len(keys))}
	for _, k := range keys {
		if k.ID == "" {
			return nil, errors.New("auth: key without id")
		}
		if _, dup := ks.byID[k.ID]; dup {
			return nil, fmt.Errorf("auth: duplicate key id %q", k.ID)
		}
		ks.byID[k.ID] = k
		if ks.signer.ID == "" && k.CanSign() {
			ks.signer = k
		}
	}
	return ks, nil
}

// KeyConfig is the environment-friendly description of a single key.
type KeyConfig struct {
	KeyID         string // default "k1"
	Secret        string // HS256 shared secret (>= 32 chars), or
	PrivateKeyPEM string // Ed25519 private key (issue + verify), or
	PublicKeyPEM  string // Ed25519 public key (verify only)
}

// KeySet builds a key set from the first non-empty of PrivateKeyPEM, PublicKeyPEM, Secret.
func (c KeyConfig) KeySet() (*KeySet, error) {
	id := c.KeyID
	if id == "" {
		id = "k1"
	}
	var (
		k   Key
		err error
	)
	switch {
	case c.PrivateKeyPEM != "":
		k, err = Ed25519Key(id, []byte(c.PrivateKeyPEM))
	case c.PublicKeyPEM != "":
		k, err = Ed25519PublicKey(id, []byte(c.PublicKeyPEM))
	case c.Secret != "":
		k, err = HMACKey(id, []byte(c.Secret))
	default:
		err = errors.New("auth: set a JWT secret or an Ed25519 key")
	}
	if err != nil {
		return nil, err
	}
	return NewKeySet(k)
}
