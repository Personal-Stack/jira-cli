package cli

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/personal-stack/jira-cli/generated"
)

type ca struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

var serial int64 = 100

func mkCert(t *testing.T, cn string, parent *ca, isCA bool, aia string, hosts ...string) *ca {
	t.Helper()
	serial++
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: cn, Organization: []string{"Company"}},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		BasicConstraintsValid: true, IsCA: isCA,
	}
	if isCA {
		tmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	} else {
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	if aia != "" {
		tmpl.IssuingCertificateURL = []string{aia}
	}
	signer, signerKey := tmpl, key
	if parent != nil {
		signer, signerKey = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return &ca{c, key}
}

// pkiEnv: an AIA "web server" (plain HTTP, like real CAs) and a TLS server.
type pkiEnv struct {
	aia   *httptest.Server
	files map[string][]byte
}

func newAIA(t *testing.T) *pkiEnv {
	p := &pkiEnv{files: map[string][]byte{}}
	p.aia = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := p.files[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(p.aia.Close)
	return p
}

func tlsServe(t *testing.T, leaf *ca, chain ...*ca) string {
	cert := tls.Certificate{Certificate: [][]byte{leaf.cert.Raw}, PrivateKey: leaf.key}
	for _, c := range chain {
		cert.Certificate = append(cert.Certificate, c.cert.Raw)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.URL
}

func runCerts(t *testing.T, dir, stdin string, interactive bool, args ...string) (int, map[string]any, string) {
	var out, errb strings.Builder
	app := NewApp("test", generated.Profiles, generated.DefaultProfile)
	app.In, app.Out, app.Err = strings.NewReader(stdin), &out, &errb
	app.Getenv = func(k string) string {
		if k == "JIRA_CONFIG" {
			return filepath.Join(dir, "none.yaml")
		}
		return ""
	}
	app.IsTerminal = func() bool { return interactive }
	code := app.Execute(context.Background(), append([]string{"certs", "fetch"}, args...))
	var res map[string]any
	json.Unmarshal([]byte(out.String()), &res)
	return code, res, errb.String()
}

func pemCount(t *testing.T, path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cns []string
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			return cns
		}
		c, _ := x509.ParseCertificate(blk.Bytes)
		cns = append(cns, c.Subject.CommonName)
	}
}

func TestCertsFetchFollowsAIAToRoot(t *testing.T) {
	p := newAIA(t)
	root := mkCert(t, "Company Root CA", nil, true, "")
	inter := mkCert(t, "Company Issuing CA", root, true, p.aia.URL+"/root.crt")
	p.files["/root.crt"] = root.cert.Raw // DER, as AD CS publishes it
	leaf := mkCert(t, "jira", inter, false, p.aia.URL+"/inter.crt", "127.0.0.1")
	p.files["/inter.crt"] = inter.cert.Raw
	dir := t.TempDir()
	out := filepath.Join(dir, "ca.pem")

	for name, srvURL := range map[string]string{
		"server sends leaf+intermediate": tlsServe(t, leaf, inter),
		"server sends leaf only":         tlsServe(t, leaf),
	} {
		os.Remove(out)
		code, res, errs := runCerts(t, dir, "", false, srvURL, "--out", out, "--yes")
		if code != 0 {
			t.Fatalf("%s: exit %d %s", name, code, errs)
		}
		if res["root_found"] != true || res["verified"] != true || res["certificates_saved"] != float64(2) {
			t.Fatalf("%s: %v", name, res)
		}
		if got := strings.Join(pemCount(t, out), ","); got != "Company Issuing CA,Company Root CA" {
			t.Fatalf("%s: saved %s (server certificate must not be saved)", name, got)
		}
		chain := res["chain"].([]any)
		if chain[len(chain)-1].(map[string]any)["source"] != "aia:"+p.aia.URL+"/root.crt" {
			t.Fatalf("%s: root source %v", name, chain[len(chain)-1])
		}
	}
}

func TestCertsFetchRejectsWrongAIACertificate(t *testing.T) {
	p := newAIA(t)
	root := mkCert(t, "Company Root CA", nil, true, "")
	impostor := mkCert(t, "Company Root CA", nil, true, "") // same name, different key
	inter := mkCert(t, "Company Issuing CA", root, true, p.aia.URL+"/root.crt")
	p.files["/root.crt"] = impostor.cert.Raw
	leaf := mkCert(t, "jira", inter, false, "", "127.0.0.1")
	dir := t.TempDir()
	out := filepath.Join(dir, "ca.pem")
	code, res, errs := runCerts(t, dir, "", false, tlsServe(t, leaf, inter), "--out", out, "--yes")
	if code != 0 {
		t.Fatalf("exit %d %s", code, errs)
	}
	if res["root_found"] != false || !strings.Contains(strings.Join(toStrings(res["notes"]), " "), "no certificate there signs") {
		t.Fatalf("impostor root must be ignored: %v", res)
	}
	if got := strings.Join(pemCount(t, out), ","); got != "Company Issuing CA" {
		t.Fatalf("saved %s", got)
	}
	if res["verified"] != true {
		t.Fatalf("the intermediate alone must still verify: %v", res)
	}
}

func TestCertsFetchConfirmationAndDryRun(t *testing.T) {
	root := mkCert(t, "Company Root CA", nil, true, "")
	leaf := mkCert(t, "jira", root, false, "", "127.0.0.1")
	srv := tlsServe(t, leaf, root)
	dir := t.TempDir()
	out := filepath.Join(dir, "ca.pem")

	if code, _, errs := runCerts(t, dir, "", false, srv, "--out", out); code != 9 || !strings.Contains(errs, "confirmation_required") {
		t.Fatalf("unattended without --yes must be refused: %d %s", code, errs)
	}
	if code, res, _ := runCerts(t, dir, "", false, srv, "--out", out, "--dry-run"); code != 0 || res["would_save_to"] != out {
		t.Fatalf("dry-run: %d %v", code, res)
	}
	if code, _, _ := runCerts(t, dir, "no\n", true, srv, "--out", out); code != 9 {
		t.Fatalf("answer 'no' must decline: %d", code)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("file written without confirmation")
	}
	code, res, errs := runCerts(t, dir, "yes\n", true, srv, "--out", out)
	if code != 0 || res["verified"] != true || !strings.Contains(errs, "sha256:") {
		t.Fatalf("interactive yes: %d %v %s", code, res, errs)
	}
}

func TestCertsFetchSelfSignedServer(t *testing.T) {
	self := mkCert(t, "jira-selfsigned", nil, true, "")
	self.cert.DNSNames = nil
	// re-issue with an IP SAN so verification can succeed
	self = func() *ca {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(999), Subject: pkix.Name{CommonName: "jira-selfsigned"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
			KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
		der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		c, _ := x509.ParseCertificate(der)
		return &ca{c, key}
	}()
	dir := t.TempDir()
	out := filepath.Join(dir, "ca.pem")
	code, res, errs := runCerts(t, dir, "", false, tlsServe(t, self), "--out", out, "--yes")
	if code != 0 || res["certificates_saved"] != float64(1) || res["verified"] != true {
		t.Fatalf("self-signed: %d %v %s", code, res, errs)
	}
}

func toStrings(v any) []string {
	var out []string
	if a, ok := v.([]any); ok {
		for _, x := range a {
			out = append(out, x.(string))
		}
	}
	return out
}
