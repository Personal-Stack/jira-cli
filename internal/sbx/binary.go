package sbx

import (
	"bytes"
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/personal-stack/jira-cli/internal/release"
)

// Binary sources, in the order "auto" tries them.
const (
	SourceSelf    = "self"    // this executable, when it is linux/<arch>
	SourceGo      = "go"      // go build from a jira-cli clone
	SourceRelease = "release" // the GitHub release of this version, SHA-256 verified
	SourceFile    = "file"    // a file the user names
)

// BinaryRequest describes where to get a linux jiracli binary from.
type BinaryRequest struct {
	Source  string // auto, self, go, release, file
	Arch    string // GOARCH of the sandbox (= the host's, sandboxes are VMs of the host architecture)
	Version string // this CLI's version
	Clone   string // jira-cli clone for SourceGo ("" = search from the working directory)
	File    string // for SourceFile
	Dir     string // scratch directory for downloads and builds
	Log     func(string)
}

// GetBinary returns the path of a verified linux/<arch> jiracli binary and the
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
	return "", "", fmt.Errorf("no linux/%s jiracli binary:\n  %s", r.Arch, strings.Join(errs, "\n  "))
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
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags", "-s -w -X main.version="+ver, "-o", out, "./cmd/jiracli")
	cmd.Dir = clone
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+r.Arch)
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %v: %s", err, strings.TrimSpace(string(b)))
	}
	return out, nil
}

func fromRelease(ctx context.Context, r BinaryRequest) (string, error) {
	if !release.IsRelease(r.Version) {
		return "", fmt.Errorf("this jiracli (%s) is not a released version", r.Version)
	}
	out := filepath.Join(r.Dir, "jira-release-"+r.Arch)
	if err := release.Fetch(ctx, r.Version, "linux", r.Arch, out, r.Log); err != nil {
		return "", err
	}
	return out, nil
}
