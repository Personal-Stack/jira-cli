package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/output"
	"github.com/personal-stack/jira-cli/internal/release"
)

// InstallURL is the installer of the latest release.
const InstallURL = "https://github.com/Personal-Stack/jira-cli/releases/latest/download/install.sh"

// executable is os.Executable (a variable for tests).
var executable = os.Executable

// selfPath returns this executable with symlinks resolved.
func selfPath() (string, error) {
	p, err := executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p, nil
}

func (a *App) upgradeCmd() *cobra.Command {
	var version string
	var check bool
	c := &cobra.Command{
		Use:   "upgrade",
		Short: "Replace this jiracli with the latest release (SHA-256 verified)",
		Long: `Downloads the latest release (or --version) for this platform from GitHub,
verifies the archive against the release's SHA256SUMS, runs the new binary
once, and only then replaces this executable in place. The configuration and
stored tokens are not touched.

--check only reports whether a newer release exists. Nothing is downloaded
unless you run this command: jiracli never checks for updates on its own.

The Docker wrapper is upgraded by running the installer again with --docker.`,
		Example: `  jiracli upgrade
  jiracli upgrade --check
  jiracli upgrade --version v0.3.0`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runUpgrade(ctxOrBackground(cmd), version, check)
		},
	}
	c.Flags().StringVar(&version, "version", "", "release to install, also an older one (default: the latest)")
	c.Flags().BoolVar(&check, "check", false, "only report whether a newer release exists")
	return c
}

func (a *App) runUpgrade(ctx context.Context, version string, check bool) error {
	format, err := a.Format()
	if err != nil {
		return err
	}
	if a.inContainer() {
		return clierr.New(clierr.LayerUsage, "upgrade_unsupported", "this jiracli runs from the Docker image").
			WithHint("upgrade the wrapper and its image with: curl -fsSL " + InstallURL + " | sh -s -- --docker")
	}
	if version != "" && !release.IsRelease(version) {
		return usageErr("--version must be a release tag such as v0.3.0, not " + version)
	}
	if version == "" && !release.IsRelease(a.Version) {
		return clierr.New(clierr.LayerUsage, "upgrade_dev_build", "this jiracli ("+a.Version+") is a development build").
			WithHint("pass --version vX.Y.Z to replace it with a release")
	}
	target := version
	if target == "" {
		if target, err = release.Latest(ctx); err != nil {
			return clierr.Wrap(clierr.LayerTransport, "release_lookup_failed", "cannot find the latest release", err).
				WithHint("pass --version vX.Y.Z, or download it from https://github.com/Personal-Stack/jira-cli/releases")
		}
	}
	res := map[string]any{"current": a.Version, "target": target}
	ui := a.newTermUI()
	if check {
		newer := !release.IsRelease(a.Version) || release.Compare(target, a.Version) > 0
		res["update_available"] = newer
		if newer {
			ui.note(a.Version + " → " + target + " is available: jiracli upgrade")
		} else {
			ui.ok("jiracli " + a.Version + " is the latest release")
		}
		return output.Write(a.Out, res, format)
	}
	if version == "" && release.Compare(target, a.Version) <= 0 {
		res["updated"] = false
		ui.ok("jiracli " + a.Version + " is the latest release; nothing to do")
		return output.Write(a.Out, res, format)
	}

	exe, err := selfPath()
	if err != nil {
		return clierr.Wrap(clierr.LayerInternal, "no_executable", "cannot find this executable", err)
	}
	dir := filepath.Dir(exe)
	_ = os.Remove(exe + ".old") // left by a previous upgrade on Windows
	staged, err := os.CreateTemp(dir, ".jiracli-upgrade-*")
	if err != nil {
		return clierr.Wrap(clierr.LayerConfig, "not_writable", "cannot write to "+dir, err).
			WithHint("install as the user who owns " + dir + " (sudo for a system folder), or reinstall: curl -fsSL " + InstallURL + " | sh")
	}
	tmp := staged.Name()
	_ = staged.Close()
	defer os.Remove(tmp) // gone after the rename; cleans up on failure

	ui.note(fmt.Sprintf("downloading jiracli %s for %s/%s", target, runtime.GOOS, runtime.GOARCH))
	if err := release.Fetch(ctx, target, runtime.GOOS, runtime.GOARCH, tmp, nil); err != nil {
		return clierr.Wrap(clierr.LayerTransport, "download_failed", "jiracli "+target+" was not installed", err)
	}
	// Run it before it replaces anything: a broken binary never replaces a
	// working one.
	out, err := exec.CommandContext(ctx, tmp, "--version").Output()
	if err != nil || !strings.Contains(string(out), target) {
		return clierr.Wrap(clierr.LayerInternal, "verify_failed", "the downloaded jiracli "+target+" does not run here; nothing was replaced", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out))))
	}
	if runtime.GOOS == "windows" { // a running .exe cannot be overwritten, only renamed
		if err := os.Rename(exe, exe+".old"); err != nil {
			return clierr.Wrap(clierr.LayerConfig, "replace_failed", "cannot replace "+exe, err)
		}
	}
	if err := os.Rename(tmp, exe); err != nil {
		if runtime.GOOS == "windows" {
			_ = os.Rename(exe+".old", exe)
		}
		return clierr.Wrap(clierr.LayerConfig, "replace_failed", "cannot replace "+exe, err)
	}
	res["updated"], res["path"] = true, exe
	ui.ok(fmt.Sprintf("jiracli %s → %s (%s)", a.Version, target, exe))
	return output.Write(a.Out, res, format)
}
