package config

import (
	"bytes"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/personal-stack/jira-cli/internal/testpki"
)

func TestParseCABundleFormats(t *testing.T) {
	p1, p2 := testpki.New(t), testpki.New(t)
	der1, _ := pem.Decode(p1.CAPEM)
	der2, _ := pem.Decode(p2.CAPEM)
	_, _, keyPEM := p1.Issue(t, "leaf", "localhost")

	cases := []struct {
		name, format, errPart string
		in                    []byte
		n                     int
	}{
		{"pem single", "pem", "", p1.CAPEM, 1},
		{"pem two", "pem", "", append(append([]byte{}, p1.CAPEM...), p2.CAPEM...), 2},
		{"pem with comments (macOS/Windows exports)", "pem", "", append([]byte("Bag Attributes\n  friendlyName: Company Root CA\n"), p1.CAPEM...), 1},
		{"der single (.cer/.cert)", "der", "", der1.Bytes, 1},
		{"der concatenated", "der", "", append(append([]byte{}, der1.Bytes...), der2.Bytes...), 2},
		{"pkcs7 pem", "pkcs7", "openssl pkcs7 -print_certs", []byte("-----BEGIN PKCS7-----\nMIIB\n-----END PKCS7-----\n"), 0},
		{"private key", "pem", "private key", keyPEM, 0},
		{"garbage", "unknown", "neither a PEM nor a DER", []byte("hello world"), 0},
		{"empty pem", "pem", "no CERTIFICATE blocks", []byte("-----BEGIN NOTHING-----\n-----END NOTHING-----\n"), 0},
	}
	for _, c := range cases {
		certs, format, err := ParseCABundle(c.in, "ca.cert")
		if format != c.format {
			t.Errorf("%s: format %q, want %q", c.name, format, c.format)
		}
		if c.errPart == "" && (err != nil || len(certs) != c.n) {
			t.Errorf("%s: %d certs, err %v", c.name, len(certs), err)
		}
		if c.errPart != "" && (err == nil || !strings.Contains(err.Error(), c.errPart)) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.errPart, err)
		}
		if bytes.Contains([]byte(errString(err)), keyPEM[30:60]) {
			t.Errorf("%s: key material in error", c.name)
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
