package sbx

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Binary sources, in the order "auto" tries them.
const (
	SourceSelf    = "self"    // this executable, when it is linux/<arch>
	SourceGo      = "go"      // go build from a jira-cli clone
	SourceRelease = "release" // the GitHub release of this version, SHA-256 verified
	SourceFile    = "file"    // a file the user names
)

// ReleaseBase is where release assets are downloaded from (a variable for tests).
var ReleaseBase = "https://github.com/Personal-Stack/jira-cli/releases/download"

// BinaryRequest describes where to get a linux jira binary from.
type BinaryRequest struct {
	Source  string // auto, self, go, release, file
	Arch    string // GOARCH of the sandbox (= the host's, sandboxes are VMs of the host architecture)
	Version string // this CLI's version
	Clone   string // jira-cli clone for SourceGo ("" = search from the working directory)
	File    string // for SourceFile
	Dir     string // scratch directory for downloads and builds
	Log     func(string)
}

// GetBinary returns the path of a verified linux/<arch> jira binary and the
// source that produced it.
func GetBinary(ctx context.Context, r BinaryRequest) (path, source string, err error) {
	if r.Log == nil {
		r.Log = func(string) {}
	}
	try := []string{r.Source}
	if r.Source == "" || r.Source == "auto" {
		try = []string{SourceSelf, SourceGo, SourceRelease}
	}
	var errs []string
	for _, s := range try {
		var p string
		switch s {
		case SourceSelf:
			p, err = fromSelf(r)
		case SourceGo:
			p, err = fromGo(ctx, r)
		case SourceRelease:
			p, err = fromRelease(ctx, r)
		case SourceFile:
			p, err = r.File, nil
			if p == "" {
				err = errors.New("no file given")
			}
		default:
			return "", "", fmt.Errorf("unknown binary source %q (auto, self, go, release, file)", s)
		}
		if err == nil {
			err = CheckLinuxBinary(p, r.Arch)
		}
		if err == nil {
			return p, s, nil
		}
		errs = append(errs, s+": "+err.Error())
	}
	return "", "", fmt.Errorf("no linux/%s jira binary:\n  %s", r.Arch, strings.Join(errs, "\n  "))
}

// CheckLinuxBinary verifies that path is a linux ELF executable for arch,
// so a macOS binary or the wrong architecture is never put in a sandbox.
func CheckLinuxBinary(path, arch string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("%s is not a Linux binary", path)
	}
	defer f.Close()
	want := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[arch]
	if want == 0 {
		return fmt.Errorf("unsupported architecture %q (amd64, arm64)", arch)
	}
	if f.Machine != want || f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN {
		return fmt.Errorf("%s is a %s binary, not linux/%s", path, f.Machine, arch)
	}
	return nil
}

func fromSelf(r BinaryRequest) (string, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != r.Arch {
		return "", fmt.Errorf("this jira is %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return os.Executable()
}

// FindClone returns the jira-cli clone containing dir, or "".
func FindClone(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if b, err := os.ReadFile(filepath.Join(d, "go.mod")); err == nil {
			if bytes.HasPrefix(b, []byte("module github.com/personal-stack/jira-cli\n")) {
				return d
			}
			return ""
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

func fromGo(ctx context.Context, r BinaryRequest) (string, error) {
	clone := r.Clone
	if clone == "" {
		wd, _ := os.Getwd()
		clone = FindClone(wd)
	}
	if clone == "" {
		return "", errors.New("not inside a jira-cli clone (--source-dir)")
	}
	if _, err := exec.LookPath("go"); err != nil {
		return "", errors.New("go is not installed")
	}
	ver := r.Version
	if out, err := exec.CommandContext(ctx, "git", "-C", clone, "describe", "--tags", "--always", "--dirty").Output(); err == nil {
		ver = strings.TrimSpace(string(out))
	}
	out := filepath.Join(r.Dir, "jira-go-"+r.Arch)
	r.Log(fmt.Sprintf("building jira %s for linux/%s with Go from %s", ver, r.Arch, clone))
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags", "-s -w -X main.version="+ver, "-o", out, "./cmd/jira")
	cmd.Dir = clone
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+r.Arch)
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %v: %s", err, strings.TrimSpace(string(b)))
	}
	return out, nil
}

func isRelease(v string) bool {
	return semverRe.MatchString(v) && strings.HasPrefix(v, "v") && !strings.Contains(v, "-g") && !strings.HasSuffix(v, "-dirty")
}

func fromRelease(ctx context.Context, r BinaryRequest) (string, error) {
	if !isRelease(r.Version) {
		return "", fmt.Errorf("this jira (%s) is not a released version", r.Version)
	}
	asset := fmt.Sprintf("jira-cli_%s_linux_%s.tar.gz", strings.TrimPrefix(r.Version, "v"), r.Arch)
	base := ReleaseBase + "/" + r.Version + "/"
	r.Log("downloading " + base + asset)
	sums, err := download(ctx, base+"SHA256SUMS", 1<<20)
	if err != nil {
		return "", err
	}
	want := ""
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			want = f[0]
		}
	}
	if want == "" {
		return "", fmt.Errorf("%s is not listed in SHA256SUMS", asset)
	}
	archive, err := download(ctx, base+asset, 200<<20)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return "", fmt.Errorf("checksum mismatch for %s (expected %s, got %s)", asset, want, got)
	}
	out := filepath.Join(r.Dir, "jira-release-"+r.Arch)
	if err := extractJira(archive, out); err != nil {
		return "", fmt.Errorf("%s: %w", asset, err)
	}
	return out, nil
}

func download(ctx context.Context, url string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "jira-cli setup-sbx")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("GET %s: response larger than %d bytes", url, limit)
	}
	return b, nil
}

func extractJira(archive []byte, out string) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return errors.New("no jira binary in the archive")
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == "jira" {
			f, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, io.LimitReader(tr, 200<<20))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			return err
		}
	}
}
