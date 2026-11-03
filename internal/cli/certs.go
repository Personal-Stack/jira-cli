package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/config"
	"github.com/personal-stack/jira-cli/internal/output"
	"github.com/personal-stack/jira-cli/internal/transport"
)

type certInfo struct {
	Subject   string    `json:"subject"`
	Issuer    string    `json:"issuer"`
	SHA256    string    `json:"sha256"`
	NotAfter  time.Time `json:"not_after"`
	IsCA      bool      `json:"ca"`
	Root      bool      `json:"self_signed_root"`
	Source    string    `json:"source"`
	SavedInto bool      `json:"saved"`
}

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

func (a *App) certsCmd() *cobra.Command {
	c := &cobra.Command{Use: "certs", Short: "Fetch and inspect the TLS certificates of a Jira server"}
	var out string
	fetch := &cobra.Command{
		Use:   "fetch [https://jira.example.com]",
		Short: "Download the CA certificates of a server into a ca_bundle file",
		Long: `Downloads the CA certificate chain of a Jira server and saves it as a PEM
file you can use as tls.ca_bundle. Without a URL, the selected instance's
base_url is used (with its proxy and client-certificate settings).

The server's own certificate is never saved, only its CA chain. If the
server omits its issuers (common: the root is rarely sent), the
"CA Issuers" (AIA) URLs in the certificates are followed, and a downloaded
certificate is kept only if it cryptographically signs the certificate
below it.

TRUST ON FIRST USE: the certificates come from the network you are about
to trust. Compare the SHA-256 fingerprint of the root with the one your
IT/PKI team publishes before relying on it. The command asks for
confirmation; --yes skips it (scripts).

No credentials are sent. After saving, a verified TLS handshake confirms
the file works.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := a.Format()
			if err != nil {
				return err
			}
			// Instance settings (proxy, client certificate) when available.
			in := &config.Instance{}
			target := ""
			if inst, _, ierr := a.Instance(); ierr == nil {
				in, target = inst, inst.BaseURL
			} else if len(args) == 0 {
				return ierr
			}
			if len(args) == 1 {
				target = args[0]
				if !strings.Contains(target, "://") {
					target = "https://" + target
				}
			}
			u, err := url.Parse(target)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				return usageErr("need an https URL, e.g. https://jira.company.internal")
			}
			probe := *in
			probe.TLS.CABundle = "" // read the chain regardless of the current bundle
			chain, notes, err := transport.FetchChain(ctxOrBackground(cmd), u.String(), &probe, durationOr(a.timeout, 30*time.Second))
			if err != nil {
				return clierr.Classify(err)
			}

			// Everything except the server's own (leaf) certificate is a CA
			// candidate; a self-signed server certificate is pinned as-is.
			var infos []certInfo
			var pemOut bytes.Buffer
			saved := 0
			for i, cc := range chain {
				ci := certInfo{
					Subject: cc.Cert.Subject.String(), Issuer: cc.Cert.Issuer.String(),
					SHA256: fingerprint(cc.Cert.Raw), NotAfter: cc.Cert.NotAfter.UTC(),
					IsCA: cc.Cert.IsCA, Root: transport.SelfSigned(cc.Cert), Source: cc.Source,
				}
				if i > 0 || len(chain) == 1 && ci.Root {
					ci.SavedInto = true
					saved++
					_ = pem.Encode(&pemOut, &pem.Block{Type: "CERTIFICATE", Bytes: cc.Cert.Raw})
				}
				infos = append(infos, ci)
			}
			if saved == 0 {
				return clierr.New(clierr.LayerCertificate, "no_ca_certificates",
					"the server sent only its own certificate and no issuer could be downloaded").
					WithHint("export the root CA from your browser or ask IT (see docs/getting-started.md, section 4); details: " + strings.Join(notes, "; "))
			}
			hasRoot := infos[len(infos)-1].Root
			if !hasRoot {
				notes = append(notes, "the root CA was not available; the saved file ends at an intermediate CA, which works as long as that intermediate is valid")
			}

			if out == "" {
				out = filepath.Join(filepath.Dir(a.ConfigPath()), u.Hostname()+"-ca.pem")
			}
			result := map[string]any{"url": u.String(), "chain": infos, "notes": notes, "root_found": hasRoot}
			if a.dryRun {
				result["dry_run"] = true
				result["would_save_to"] = out
				return output.Write(a.Out, result, f)
			}
			if !a.yes {
				if !a.IsTerminal() {
					return clierr.New(clierr.LayerSafety, "confirmation_required",
						"saving a CA certificate fetched from the network requires confirmation").
						WithHint("review the fingerprints (e.g. with --dry-run), then re-run with --yes")
				}
				fmt.Fprintf(a.Err, "Certificate chain of %s:\n", u.Host)
				for i, ci := range infos {
					mark := "  (server certificate, not saved)"
					if ci.SavedInto {
						mark = "  -> will be saved"
					}
					fmt.Fprintf(a.Err, "[%d] %s%s\n    issuer:  %s\n    sha256:  %s\n    expires: %s  source: %s\n",
						i, ci.Subject, mark, ci.Issuer, ci.SHA256, ci.NotAfter.Format("2006-01-02"), ci.Source)
				}
				for _, n := range notes {
					fmt.Fprintf(a.Err, "note: %s\n", n)
				}
				fmt.Fprintf(a.Err, "\nThese certificates come from the network. Compare the root's SHA-256 with your IT/PKI team's.\nSave %d CA certificate(s) to %s and trust them? Type yes: ", saved, out)
				line, _ := bufio.NewReader(a.In).ReadString('\n')
				if strings.TrimSpace(strings.ToLower(line)) != "yes" {
					return clierr.New(clierr.LayerSafety, "confirmation_declined", "not saved")
				}
			}
			if err := writeFileAtomic(out, pemOut.Bytes(), 0o644); err != nil {
				return clierr.Wrap(clierr.LayerConfig, "write_failed", "cannot write "+out, err)
			}
			result["saved_to"], result["certificates_saved"] = out, saved

			// Prove it: a verified handshake using system roots + the new file.
			check := *in
			check.TLS.CABundle = out
			verified := false
			if hc, err := transport.New(&check, durationOr(a.timeout, 30*time.Second)); err == nil {
				req, _ := httpHead(ctxOrBackground(cmd), u.Scheme+"://"+u.Host+"/")
				resp, err := hc.Do(req)
				if resp != nil {
					resp.Body.Close()
				}
				if err == nil || clierr.Classify(err).Layer != clierr.LayerCertificate {
					verified = true
				} else {
					result["verify_error"] = clierr.Classify(err).Error()
				}
			}
			result["verified"] = verified
			result["config"] = "tls:\n  ca_bundle: " + out
			return output.Write(a.Out, result, f)
		},
	}
	fetch.Flags().StringVar(&out, "out", "", "output file (default: <config dir>/<host>-ca.pem)")
	c.AddCommand(fetch)
	return c
}

func httpHead(ctx context.Context, u string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err == nil {
		req.Header.Set("User-Agent", "jira-cli certs fetch")
	}
	return req, err
}

func writeFileAtomic(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
