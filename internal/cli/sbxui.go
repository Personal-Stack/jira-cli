package cli

import (
	"fmt"
	"io"
	"strings"
)

// sbxUI styles setup-sbx's human output on stderr: sections, ✓ / ! marks,
// highlighted commands. Colors only on a terminal, and never with --no-color,
// NO_COLOR or TERM=dumb. stdout (the machine-readable result) is never styled.
type sbxUI struct {
	w     io.Writer
	color bool
}

func (a *App) newSbxUI() *sbxUI {
	color := a.IsTerminal() && !a.noColor && a.Getenv("NO_COLOR") == "" && a.Getenv("TERM") != "dumb"
	return &sbxUI{w: a.Err, color: color}
}

func (u *sbxUI) paint(code, s string) string {
	if !u.color || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (u *sbxUI) bold(s string) string   { return u.paint("1", s) }
func (u *sbxUI) dim(s string) string    { return u.paint("2", s) }
func (u *sbxUI) green(s string) string  { return u.paint("32", s) }
func (u *sbxUI) yellow(s string) string { return u.paint("33", s) }
func (u *sbxUI) cyan(s string) string   { return u.paint("36", s) }

func (u *sbxUI) println(s string) { fmt.Fprintln(u.w, s) }

func (u *sbxUI) title(sub string) {
	u.println(u.paint("1;36", "jira setup-sbx") + " " + u.dim("· "+sub))
}

func (u *sbxUI) section(name string) { u.println("\n" + u.bold(u.cyan("▸ ")+name)) }

func (u *sbxUI) ok(s string)   { u.println("  " + u.green("✓") + " " + s) }
func (u *sbxUI) warn(s string) { u.println("  " + u.yellow("!") + " " + s) }
func (u *sbxUI) note(s string) { u.println("  " + u.dim(s)) }

// kv prints an aligned "key  value" line (summaries).
func (u *sbxUI) kv(k, v string) { u.println("  " + u.dim(fmt.Sprintf("%-10s", k)) + " " + v) }

// cmd renders a shell command to copy.
func (u *sbxUI) cmd(s string) string { return u.dim("$ ") + u.bold(s) }

// result prints a note from the generator: "warning: ..." as !, the rest as ✓.
func (u *sbxUI) result(n string) {
	if w, ok := strings.CutPrefix(n, "warning: "); ok {
		u.warn(w)
		return
	}
	u.ok(n)
}

// question renders a prompt with its default.
func (u *sbxUI) question(q, cur string) string {
	s := u.cyan("?") + " " + u.bold(q)
	if cur != "" {
		s += " " + u.dim("["+cur+"]")
	}
	return s + ": "
}
