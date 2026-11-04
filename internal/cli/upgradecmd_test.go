package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/personal-stack/jira-cli/generated"
	"github.com/personal-stack/jira-cli/internal/release"
)

// fakeReleases serves a latest-release answer and, for each version, an
// archive whose jiracli is a script printing that version.
func fakeReleases(t *testing.T, latest string, tamper bool) {
	t.Helper()
	archives := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest" {
			_, _ = w.Write([]byte(`{"tag_name":"` + latest + `"}`))
			return
		}
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		v := parts[0]
		asset := release.Asset(v, runtime.GOOS, runtime.GOARCH)
		if archives[v] == nil {
			script := []byte("#!/bin/sh\necho jiracli version " + v + "\n")
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			tw := tar.NewWriter(gz)
			_ = tw.WriteHeader(&tar.Header{Name: "jira-cli/jiracli", Mode: 0o755, Size: int64(len(script)), Typeflag: tar.TypeReg})
			_, _ = tw.Write(script)
			_, _ = tw.Close(), gz.Close()
			archives[v] = buf.Bytes()
		}
		switch parts[1] {
		case "SHA256SUMS":
			sum := sha256.Sum256(archives[v])
			_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n"))
		case asset:
			b := archives[v]
			if tamper {
				b = append(append([]byte{}, b...), 'x')
			}
			_, _ = w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	oldBase, oldLatest := release.Base, release.LatestURL
	release.Base, release.LatestURL = srv.URL, srv.URL+"/latest"
	t.Cleanup(func() { release.Base, release.LatestURL = oldBase, oldLatest })
}

// fakeExe is the "installed" jiracli that upgrade replaces.
func fakeExe(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "jiracli")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := executable
	executable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executable = old })
	return exe
}

func runUpgrade(t *testing.T, version string, vars map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	app := NewApp(version, generated.Profiles, generated.DefaultProfile)
	app.In, app.Out, app.Err = strings.NewReader(""), &out, &errb
	app.IsTerminal = func() bool { return false }
	app.Getenv = func(k string) string { return vars[k] }
	code := app.Execute(context.Background(), append([]string{"--config", filepath.Join(t.TempDir(), "none.yaml"), "upgrade"}, args...))
	return code, out.String(), errb.String()
}

func TestUpgradeReplacesThisExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	fakeReleases(t, "v0.4.0", false)
	exe := fakeExe(t)

	code, out, errOut := runUpgrade(t, "v0.3.0", nil, "--check")
	if code != 0 || !strings.Contains(out, `"update_available": true`) || !strings.Contains(out, `"target": "v0.4.0"`) {
		t.Fatalf("check: exit %d\n%s\n%s", code, out, errOut)
	}
	if b, _ := os.ReadFile(exe); string(b) != "#!/bin/sh\necho old\n" {
		t.Fatal("--check changed the executable")
	}

	code, out, errOut = runUpgrade(t, "v0.3.0", nil)
	if code != 0 || !strings.Contains(out, `"updated": true`) {
		t.Fatalf("upgrade: exit %d\n%s\n%s", code, out, errOut)
	}
	if b, _ := os.ReadFile(exe); !strings.Contains(string(b), "jiracli version v0.4.0") {
		t.Fatalf("not replaced: %s", b)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".jiracli-upgrade-*")); len(left) != 0 {
		t.Fatalf("staged files left: %v", left)
	}

	// Already the latest: nothing happens.
	if code, out, _ = runUpgrade(t, "v0.4.0", nil); code != 0 || !strings.Contains(out, `"updated": false`) {
		t.Fatalf("up to date: exit %d\n%s", code, out)
	}
	// An explicit older version is a downgrade the user asked for.
	if code, _, errOut = runUpgrade(t, "v0.4.0", nil, "--version", "v0.3.0"); code != 0 {
		t.Fatalf("downgrade: exit %d\n%s", code, errOut)
	}
	if b, _ := os.ReadFile(exe); !strings.Contains(string(b), "v0.3.0") {
		t.Fatal("not downgraded")
	}
}

func TestUpgradeRefusesWhatItCannotDoSafely(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	fakeReleases(t, "v0.4.0", true)
	exe := fakeExe(t)

	code, _, errOut := runUpgrade(t, "v0.3.0", nil)
	if code == 0 || !strings.Contains(errOut, "checksum mismatch") {
		t.Fatalf("tampered archive: exit %d\n%s", code, errOut)
	}
	if b, _ := os.ReadFile(exe); string(b) != "#!/bin/sh\necho old\n" {
		t.Fatal("a failed upgrade changed the executable")
	}
	if code, _, errOut = runUpgrade(t, "v0.3.0-2-gabc1234", nil); code != 2 || !strings.Contains(errOut, "development build") {
		t.Fatalf("dev build: exit %d\n%s", code, errOut)
	}
	if code, _, errOut = runUpgrade(t, "v0.3.0", map[string]string{"JIRA_CLI_CONTAINER": "1"}); code != 2 || !strings.Contains(errOut, "--docker") {
		t.Fatalf("container: exit %d\n%s", code, errOut)
	}
	if code, _, _ = runUpgrade(t, "v0.3.0", nil, "--version", "latest"); code != 2 {
		t.Fatalf("bad --version: exit %d", code)
	}
}

func TestReleaseCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"v0.4.0", "v0.3.9", 1}, {"v0.10.0", "v0.9.0", 1}, {"v1.0.0-rc.1", "v1.0.0", -1}, {"v0.3.0", "v0.3.0", 0}} {
		if got := release.Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%s, %s) = %d", c.a, c.b, got)
		}
	}
}
