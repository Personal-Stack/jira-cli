package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
)

func isTLSError(err error) bool {
	var (
		ua x509.UnknownAuthorityError
		he x509.HostnameError
		ci x509.CertificateInvalidError
		ve *tls.CertificateVerificationError
		rh tls.RecordHeaderError
	)
	return errors.As(err, &ua) || errors.As(err, &he) || errors.As(err, &ci) || errors.As(err, &ve) || errors.As(err, &rh)
}
