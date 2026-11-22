package moadian

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
)

// Signer holds the taxpayer's signing identity. The private key and certificate are secrets of the deployment: load them
// from files or a secret store at start-up, never from the repository, and never log them.
type Signer interface {
	// Certificate is the DER-encoded X.509 certificate that goes into the x5c header.
	Certificate() []byte
	// Sign returns the RSASSA-PKCS1-v1_5 SHA-256 signature of data.
	Sign(data []byte) ([]byte, error)
}

// NewKeySigner parses a PEM certificate and a PEM private key (PKCS#8, as the document loads it; PKCS#1 is accepted too).
func NewKeySigner(certPEM, keyPEM []byte) (*KeySigner, error) {
	cb, _ := pem.Decode(certPEM)
	if cb == nil || cb.Type != "CERTIFICATE" {
		return nil, errors.New("moadian: the certificate is not PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, wrap("certificate", err)
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return nil, errors.New("moadian: the private key is not PEM")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(kb.Bytes); err == nil {
		var ok bool
		if key, ok = k.(*rsa.PrivateKey); !ok {
			return nil, errNotRSA
		}
	} else if k, err := x509.ParsePKCS1PrivateKey(kb.Bytes); err == nil {
		key = k
	} else {
		return nil, errors.New("moadian: the private key is neither PKCS#8 nor PKCS#1")
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok || pub.N.Cmp(key.N) != 0 || pub.E != key.E {
		return nil, errors.New("moadian: the certificate does not belong to the private key")
	}
	return &KeySigner{key: key, cert: cert.Raw}, nil
}
