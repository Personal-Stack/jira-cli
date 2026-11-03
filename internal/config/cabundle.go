package config

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
)

// ParseCABundle parses a CA bundle in PEM (one or more CERTIFICATE blocks)
// or DER (one or more concatenated certificates; typical for .cer/.cert
// exports from Windows and macOS). The file extension is irrelevant.
// It returns the certificates and the detected format ("pem" or "der").
// PKCS#7 bundles and files containing private keys are rejected with a
// message explaining how to convert or what went wrong.
func ParseCABundle(b []byte, path string) ([]*x509.Certificate, string, error) {
	if bytes.Contains(b, []byte("-----BEGIN ")) {
		var certs []*x509.Certificate
		rest := b
		for {
			var blk *pem.Block
			blk, rest = pem.Decode(rest)
			if blk == nil {
				break
			}
			switch {
			case blk.Type == "CERTIFICATE" || blk.Type == "TRUSTED CERTIFICATE":
				c, err := x509.ParseCertificate(blk.Bytes)
				if err != nil {
					return nil, "pem", fmt.Errorf("%s: invalid certificate: %w", path, err)
				}
				certs = append(certs, c)
			case blk.Type == "PKCS7":
				return nil, "pkcs7", fmt.Errorf("%s is a PKCS#7 bundle; convert it with: openssl pkcs7 -print_certs -in %s -out ca.pem", path, path)
			case strings.Contains(blk.Type, "PRIVATE KEY"):
				return nil, "pem", fmt.Errorf("%s contains a private key, not a CA certificate; export only the certificate (and keep the key secret)", path)
			}
		}
		if len(certs) == 0 {
			return nil, "pem", fmt.Errorf("%s contains no CERTIFICATE blocks", path)
		}
		return certs, "pem", nil
	}
	certs, err := x509.ParseCertificates(b)
	if err == nil && len(certs) > 0 {
		return certs, "der", nil
	}
	return nil, "unknown", fmt.Errorf("%s is neither a PEM nor a DER X.509 certificate; if it is a PKCS#7 bundle (.p7b), convert it with: openssl pkcs7 -inform der -print_certs -in %s -out ca.pem", path, path)
}
