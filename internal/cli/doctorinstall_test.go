package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDoctorReportsShadowingAndTheOldJira(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executables")
	}
	e := setup(t, "")
	self := fakeExe(t)
	other := t.TempDir()
	write := func(dir, name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(other, "jiracli", "#!/bin/sh\n")
	write(other, "jira", "\x7fELF old build of github.com/personal-stack/jira-cli\n")

	e.vars["PATH"] = other + string(os.PathListSeparator) + filepath.Dir(self)
	code, out, _ := e.run("", "doctor")
	if code != 0 || !strings.Contains(out, `"name": "installation"`) || !strings.Contains(out, "'jiracli' on PATH runs "+filepath.Join(other, "jiracli")) ||
		!strings.Contains(out, "old jira-cli command") || !strings.Contains(out, "rm "+filepath.Join(other, "jira")) {
		t.Fatalf("doctor exit %d:\n%s", code, out)
	}

	// Only this jiracli, and a 'jira' from another project: nothing to report.
	write(other, "jira", "#!/bin/sh\necho another jira\n")
	e.vars["PATH"] = filepath.Dir(self) + string(os.PathListSeparator) + other
	code, out, _ = e.run("", "doctor")
	if code != 0 || strings.Contains(out, "old jira-cli") || strings.Contains(out, "runs "+filepath.Join(other, "jiracli")) {
		t.Fatalf("doctor exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, `"path": "`+self+`"`) {
		t.Fatalf("path missing:\n%s", out)
	}
}
