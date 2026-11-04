package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// installCheck is doctor's first check: which jiracli runs, and whether
// another command on PATH gets in its way. It only warns: none of this stops
// the CLI from working when run by its full path.
func (a *App) installCheck() check {
	c := check{Name: "installation", Status: "ok", Data: map[string]any{
		"version": a.Version, "platform": runtime.GOOS + "/" + runtime.GOARCH,
	}}
	exe, err := selfPath()
	if err != nil {
		c.Status, c.Detail = "warn", "cannot find this executable: "+err.Error()
		return c
	}
	c.Data["path"] = exe
	if a.inContainer() { // the Docker wrapper's container: the host's PATH is not visible
		c.Data["docker_wrapper"] = true
		return c
	}
	var warns, hints []string
	ext := map[bool]string{true: ".exe", false: ""}[runtime.GOOS == "windows"]
	path := a.Getenv("PATH")
	switch first := lookPath(path, "jiracli"+ext); {
	case first == "":
		warns = append(warns, "jiracli is not on PATH")
		hints = append(hints, "add "+filepath.Dir(exe)+" to PATH")
	case first != exe:
		warns = append(warns, "'jiracli' on PATH runs "+first+", not this one ("+exe+")")
		hints = append(hints, "remove the other one, or reorder PATH")
	}
	if old := lookPath(path, "jira"+ext); old != "" && isOldJiraCLI(old) {
		warns = append(warns, old+" is the old jira-cli command (renamed to jiracli in v0.3.0) and runs an old version")
		hints = append(hints, "remove it: rm "+old)
	}
	if len(warns) > 0 {
		c.Status, c.Detail, c.Hint = "warn", strings.Join(warns, "; "), strings.Join(hints, "; ")
	}
	return c
}

// lookPath returns the first executable file named name in the PATH list,
// symlinks resolved, or "".
func lookPath(pathList, name string) string {
	for _, dir := range filepath.SplitList(pathList) {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && (runtime.GOOS == "windows" || st.Mode().Perm()&0o111 != 0) {
			if r, err := filepath.EvalSymlinks(p); err == nil {
				return r
			}
			return p
		}
	}
	return ""
}

// isOldJiraCLI recognises jira-cli's own pre-v0.3.0 'jira': the binary
// (its module path) or the Docker wrapper (its header). Same test as
// install.sh, so a 'jira' from another project is never reported.
func isOldJiraCLI(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, 128<<20))
	return bytes.Contains(b, []byte("github.com/personal-stack/jira-cli")) || bytes.Contains(b, []byte("runs jira-cli from its Docker image"))
}
