package moadian

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"strings"
)

// CSRSubject is the subject of the certificate request. WHAT THE PORTAL REQUIRES in the subject was not found in an official text: fill the fields the Karpooshe portal's guide asks for
// (usually the taxpayer's name and national id). Only CommonName is mandatory here.
type CSRSubject struct {
	CommonName   string
	Organization string
	OrgUnit      string
	Country      string // default IR
	SerialNumber string // e.g. the national id / economic code, when the portal asks for it
}

// GenerateKeyAndCSR makes an RSA key pair (2048 bits by default; the connection document's signature is RS256, RSA keys) with the standard library only and a PKCS#10 request
// signed with SHA-256, both as PEM: the private key as PKCS#8 (what NewKeySigner reads), the request to upload on the tax portal. Nothing is stored here: the caller decides where the private
// key goes, and it must go only to a secret store or an encrypted file (mode 0600), NEVER into the database, a log or the repository.
func GenerateKeyAndCSR(s CSRSubject, bits int) (keyPEM, csrPEM []byte, err error) {
	if strings.TrimSpace(s.CommonName) == "" {
		return nil, nil, errors.New("moadian: a common name is required")
	}
	if bits == 0 {
		bits = 2048
	}
	if bits < 2048 {
		return nil, nil, errors.New("moadian: an RSA key shorter than 2048 bits is refused")
	}
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, nil, err
	}
	country := s.Country
	if country == "" {
		country = "IR"
	}
	name := pkix.Name{CommonName: s.CommonName, Country: []string{country}, SerialNumber: s.SerialNumber}
	if s.Organization != "" {
		name.Organization = []string{s.Organization}
	}
	if s.OrgUnit != "" {
		name.OrganizationalUnit = []string{s.OrgUnit}
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: name, SignatureAlgorithm: x509.SHA256WithRSA}, key)
	if err != nil {
		return nil, nil, err
	}
	pk, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}
