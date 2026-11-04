// Package release finds, downloads and verifies jira-cli release archives:
// the latest tag, the archive for a platform checked against the release's
// SHA256SUMS, and the jiracli binary inside it. Used by `jiracli upgrade`
// and by `jiracli setup-sbx` (the sandbox's linux binary).
package release

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Where releases come from (variables for tests).
var (
	Base      = "https://github.com/Personal-Stack/jira-cli/releases/download"
	LatestURL = "https://api.github.com/repos/Personal-Stack/jira-cli/releases/latest"
)

const maxArchive = 200 << 20

var semverRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)(-[0-9A-Za-z.-]+)?$`)

// IsRelease reports whether v is a release tag (vX.Y.Z or vX.Y.Z-rc.1), not
// a development build such as v0.3.0-2-gabc1234 or v0.3.0-dirty.
func IsRelease(v string) bool {
	return semverRe.MatchString(v) && !strings.Contains(v, "-g") && !strings.HasSuffix(v, "-dirty")
}

// Compare orders two release tags: -1, 0 or 1. A pre-release sorts before
// its release (v1.0.0-rc.1 < v1.0.0).
func Compare(a, b string) int {
	ma, mb := semverRe.FindStringSubmatch(a), semverRe.FindStringSubmatch(b)
	if ma == nil || mb == nil {
		return strings.Compare(a, b)
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(ma[i])
		y, _ := strconv.Atoi(mb[i])
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case ma[4] == mb[4]:
		return 0
	case ma[4] == "":
		return 1
	case mb[4] == "":
		return -1
	}
	return strings.Compare(ma[4], mb[4])
}

// Latest returns the tag of the latest release (pre-releases excluded).
func Latest(ctx context.Context) (string, error) {
	b, err := get(ctx, LatestURL, 1<<20)
	if err != nil {
		return "", err
	}
	var r struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(b, &r); err != nil || !IsRelease(r.Tag) {
		return "", fmt.Errorf("unexpected answer from %s", LatestURL)
	}
	return r.Tag, nil
}

// Asset is the archive name of a version for a platform.
func Asset(version, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("jira-cli_%s_%s_%s.%s", strings.TrimPrefix(version, "v"), goos, goarch, ext)
}

// Fetch downloads the archive of version for goos/goarch, verifies it
// against the release's SHA256SUMS and writes the jiracli binary in it to
// out (mode 0755). Nothing is written when a check fails.
func Fetch(ctx context.Context, version, goos, goarch, out string, log func(string)) error {
	if !IsRelease(version) {
		return fmt.Errorf("%s is not a release version", version)
	}
	asset := Asset(version, goos, goarch)
	base := Base + "/" + version + "/"
	if log != nil {
		log("downloading " + base + asset)
	}
	sums, err := get(ctx, base+"SHA256SUMS", 1<<20)
	if err != nil {
		return err
	}
	want := ""
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			want = f[0]
		}
	}
	if want == "" {
		return fmt.Errorf("%s is not listed in SHA256SUMS (no build for %s/%s?)", asset, goos, goarch)
	}
	archive, err := get(ctx, base+asset, maxArchive)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum mismatch for %s (expected %s, got %s)", asset, want, got)
	}
	name := "jiracli"
	if goos == "windows" {
		name += ".exe"
	}
	bin, err := extract(archive, strings.HasSuffix(asset, ".zip"), name)
	if err != nil {
		return fmt.Errorf("%s: %w", asset, err)
	}
	if err := os.WriteFile(out, bin, 0o755); err != nil {
		return err
	}
	return os.Chmod(out, 0o755) // WriteFile keeps the mode of an existing file
}

func extract(archive []byte, isZip bool, name string) ([]byte, error) {
	if isZip {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if !f.FileInfo().IsDir() && path.Base(f.Name) == name {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, maxArchive))
			}
		}
		return nil, fmt.Errorf("no %s in the archive", name)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("no %s in the archive", name)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && path.Base(h.Name) == name {
			return io.ReadAll(io.LimitReader(tr, maxArchive))
		}
	}
}

func get(ctx context.Context, url string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "jira-cli")
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
		return nil, errors.New("GET " + url + ": response too large")
	}
	return b, nil
}
