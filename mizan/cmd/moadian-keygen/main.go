// Command moadian-keygen makes an RSA key pair and a certificate request (CSR) for the Moadian portal with the Go standard library, on the machine that runs it: no website sees the key.
// The private key is written with mode 0600 and must be stored in a secret manager or an encrypted file, never in a database, a log or the repository.
//
//	go run ./cmd/moadian-keygen -cn "Company name" -o "Company" -serial 1234567890 -out ./keys
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/PouryDev/server-infrastructure/mizan/einvoice/moadian"
)

func main() {
	cn := flag.String("cn", "", "common name (required)")
	org := flag.String("o", "", "organization")
	ou := flag.String("ou", "", "organizational unit")
	serial := flag.String("serial", "", "subject serial number (national id / economic code) when the portal asks for it")
	bits := flag.Int("bits", 2048, "RSA key size (at least 2048)")
	out := flag.String("out", ".", "directory for moadian.key and moadian.csr")
	flag.Parse()
	keyPEM, csrPEM, err := moadian.GenerateKeyAndCSR(moadian.CSRSubject{CommonName: *cn, Organization: *org, OrgUnit: *ou, SerialNumber: *serial}, *bits)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	for name, data := range map[string][]byte{"moadian.key": keyPEM, "moadian.csr": csrPEM} {
		mode := os.FileMode(0o644)
		if name == "moadian.key" {
			mode = 0o600
		}
		if err := os.WriteFile(filepath.Join(*out, name), data, mode); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}
	fmt.Println("wrote", filepath.Join(*out, "moadian.key"), "(keep it secret) and", filepath.Join(*out, "moadian.csr"), "(upload it on the portal)")
}
