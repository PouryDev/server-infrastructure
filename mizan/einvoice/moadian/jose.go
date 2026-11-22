package moadian

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var b64 = base64.RawURLEncoding

// sigTLayout is the signing-time format of the JWS header: yyyy-MM-dd'T'HH:mm:ss'Z' in UTC.
const sigTLayout = "2006-01-02T15:04:05Z"

// jwsHeader is the protected header of every signature (token and invoice). The field order is the document's.
type jwsHeader struct {
	Alg  string   `json:"alg"`
	X5C  []string `json:"x5c"`
	SigT string   `json:"sigT"`
	Typ  string   `json:"typ"`
	Crit []string `json:"crit"`
	Cty  string   `json:"cty"`
}

func marshal(v any) ([]byte, error) {
	var buf bytesBuffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.trimNewline(), nil
}

type bytesBuffer struct{ b []byte }

func (w *bytesBuffer) Write(p []byte) (int, error) { w.b = append(w.b, p...); return len(p), nil }
func (w *bytesBuffer) trimNewline() []byte {
	if n := len(w.b); n > 0 && w.b[n-1] == '\n' {
		return w.b[:n-1]
	}
	return w.b
}

// signJWS builds the compact JWS: b64url(header) . b64url(payload) . b64url(RSASSA-PKCS1-v1_5 SHA-256 signature).
func signJWS(s Signer, payload []byte, now time.Time) (string, error) {
	hdr, err := marshal(jwsHeader{Alg: "RS256", X5C: []string{base64.StdEncoding.EncodeToString(s.Certificate())}, SigT: now.UTC().Format(sigTLayout),
		Typ: "jose", Crit: []string{"sigT"}, Cty: "text/plain"})
	if err != nil {
		return "", err
	}
	input := b64.EncodeToString(hdr) + "." + b64.EncodeToString(payload)
	sig, err := s.Sign([]byte(input))
	if err != nil {
		return "", err
	}
	return input + "." + b64.EncodeToString(sig), nil
}

// encryptJWE builds the compact JWE of a signed invoice: RSA-OAEP-256 for the key, A256GCM for the content, the ASCII
// of the encoded protected header as the additional authenticated data.
func encryptJWE(plain []byte, pub *rsa.PublicKey, kid string) (string, error) {
	hdr, err := marshal(struct {
		Alg string `json:"alg"`
		Enc string `json:"enc"`
		Kid string `json:"kid"`
	}{"RSA-OAEP-256", "A256GCM", kid})
	if err != nil {
		return "", err
	}
	protected := b64.EncodeToString(hdr)
	cek := make([]byte, 32) // 256-bit AES key
	iv := make([]byte, 12)  // 96-bit IV
	if _, err := rand.Read(cek); err != nil {
		return "", err
	}
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, iv, plain, []byte(protected)) // ciphertext || 16-byte tag
	ct, tag := sealed[:len(sealed)-gcm.Overhead()], sealed[len(sealed)-gcm.Overhead():]
	ek, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, cek, nil)
	if err != nil {
		return "", err
	}
	return protected + "." + b64.EncodeToString(ek) + "." + b64.EncodeToString(iv) + "." + b64.EncodeToString(ct) + "." + b64.EncodeToString(tag), nil
}

// KeySigner signs with an RSA private key and carries its certificate.
type KeySigner struct {
	key  *rsa.PrivateKey
	cert []byte
}

var errNotRSA = errors.New("moadian: the key must be an RSA key")

// Certificate returns the DER certificate placed in the x5c header.
func (k *KeySigner) Certificate() []byte { return k.cert }

// Sign is RSASSA-PKCS1-v1_5 with SHA-256.
func (k *KeySigner) Sign(data []byte) ([]byte, error) {
	sum := sha256.Sum256(data)
	return rsa.SignPKCS1v15(rand.Reader, k.key, crypto.SHA256, sum[:])
}

func wrap(msg string, err error) error { return fmt.Errorf("moadian: %s: %w", msg, err) }
