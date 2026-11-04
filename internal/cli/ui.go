package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/personal-stack/jira-cli/internal/clierr"
)

// termUI styles the human output of guided commands (config setup,
// setup-sbx) on stderr: sections, ✓ / ! marks, highlighted commands. Colors only on a terminal, and never with --no-color,
// NO_COLOR or TERM=dumb. stdout (the machine-readable result) is never styled.
type termUI struct {
	w     io.Writer
	color bool
}

func (a *App) newTermUI() *termUI {
	color := a.IsTerminal() && !a.noColor && a.Getenv("NO_COLOR") == "" && a.Getenv("TERM") != "dumb"
	return &termUI{w: a.Err, color: color}
}

func (u *termUI) paint(code, s string) string {
	if !u.color || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (u *termUI) bold(s string) string   { return u.paint("1", s) }
func (u *termUI) dim(s string) string    { return u.paint("2", s) }
func (u *termUI) green(s string) string  { return u.paint("32", s) }
func (u *termUI) yellow(s string) string { return u.paint("33", s) }
func (u *termUI) cyan(s string) string   { return u.paint("36", s) }

func (u *termUI) println(s string) { fmt.Fprintln(u.w, s) }

func (u *termUI) title(command, sub string) {
	u.println(u.paint("1;36", command) + " " + u.dim("· "+sub))
}

func (u *termUI) section(name string) { u.println("\n" + u.bold(u.cyan("▸ ")+name)) }

func (u *termUI) ok(s string)   { u.println("  " + u.green("✓") + " " + s) }
func (u *termUI) warn(s string) { u.println("  " + u.yellow("!") + " " + s) }
func (u *termUI) bad(s string)  { u.println("  " + u.paint("31", "✗") + " " + s) }
func (u *termUI) note(s string) { u.println("  " + u.dim(s)) }

// kv prints an aligned "key  value" line (summaries).
func (u *termUI) kv(k, v string) { u.println("  " + u.dim(fmt.Sprintf("%-10s", k)) + " " + v) }

// cmd renders a shell command to copy.
func (u *termUI) cmd(s string) string { return u.dim("$ ") + u.bold(s) }

// question renders a prompt with its default.
func (u *termUI) question(q, cur string) string {
	s := u.cyan("?") + " " + u.bold(q)
	if cur != "" {
		s += " " + u.dim("["+cur+"]")
	}
	return s + ": "
}

// form asks questions until the first error (Ctrl+C, end of input), which
// it keeps in err, so a sequence of questions needs one error check.
type form struct {
	p   *prompter
	ui  *termUI
	err error
}

// text asks until validate (nil: anything) accepts the answer.
func (f *form) text(dst *string, q string, validate func(string) error) {
	for f.err == nil {
		v, err := f.p.ask(q, *dst)
		if f.err = err; err != nil {
			return
		}
		if validate != nil {
			if verr := validate(v); verr != nil {
				f.ui.warn(verr.Error())
				continue
			}
		}
		*dst = v
		return
	}
}

func (f *form) yesno(dst *bool, q string) {
	if f.err == nil {
		*dst, f.err = f.p.yn(q, *dst)
	}
}

func optional(check func(string) error) func(string) error {
	return func(v string) error {
		if v == "" {
			return nil
		}
		return check(v)
	}
}

// prompter asks on stderr and reads answers in a goroutine, so Ctrl+C (a
// cancelled ctx) ends a prompt at once instead of waiting for Enter. The
// goroutine reads one line per question and nothing in between, so a hidden
// token prompt (term.ReadPassword) can use the terminal between questions.
type prompter struct {
	ctx   context.Context
	in    io.Reader
	req   chan struct{}
	lines chan string
	eof   bool
	ui    *termUI
}

// form returns a form on the App's one prompter: all questions of a run
// read from the same reader, because a reader buffers ahead.
func (a *App) form(ctx context.Context, ui *termUI) *form {
	if a.prompts == nil {
		a.prompts = newPrompter(ctx, a.In, ui)
	}
	a.prompts.ui = ui
	return &form{p: a.prompts, ui: ui}
}

func newPrompter(ctx context.Context, r io.Reader, ui *termUI) *prompter {
	p := &prompter{ctx: ctx, in: r, req: make(chan struct{}, 1), lines: make(chan string), ui: ui}
	go func() {
		defer close(p.lines) // later prompts see the end of input
		br := bufio.NewReader(r)
		for {
			select {
			case <-p.req:
			case <-ctx.Done():
				return
			}
			line, err := br.ReadString('\n')
			if line == "" && err != nil {
				return
			}
			select {
			case p.lines <- line:
			case <-ctx.Done():
				return
			}
		}
	}()
	return p
}

// line reads the next answer.
func (p *prompter) line() (string, error) {
	if p.eof {
		return "", clierr.New(clierr.LayerUsage, "no_answer", "no answer (end of input)")
	}
	select {
	case p.req <- struct{}{}:
	case <-p.ctx.Done():
		fmt.Fprintln(p.ui.w)
		return "", clierr.Interrupted()
	}
	select {
	case <-p.ctx.Done():
		fmt.Fprintln(p.ui.w)
		return "", clierr.Interrupted()
	case line, ok := <-p.lines:
		if !ok {
			p.eof = true
			return "", clierr.New(clierr.LayerUsage, "no_answer", "no answer (end of input)")
		}
		return line, nil
	}
}

// ask shows cur as the default: Enter keeps it, "-" clears it.
func (p *prompter) ask(q, cur string) (string, error) {
	fmt.Fprint(p.ui.w, p.ui.question(q, cur))
	line, err := p.line()
	if err != nil {
		return "", err
	}
	switch v := strings.TrimSpace(line); v {
	case "":
		return cur, nil
	case "-":
		return "", nil
	default:
		return v, nil
	}
}

// secret asks for a value that is not shown: hidden input on a terminal,
// the next line otherwise.
func (p *prompter) secret(q string) (string, error) {
	fmt.Fprint(p.ui.w, p.ui.question(q, ""))
	if f, ok := p.in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(p.ui.w)
		return strings.TrimSpace(string(b)), err
	}
	line, err := p.line()
	return strings.TrimSpace(line), err
}

func (p *prompter) yn(q string, cur bool) (bool, error) {
	hint := map[bool]string{true: "Y/n", false: "y/N"}[cur]
	for {
		v, err := p.ask(q+" "+p.ui.dim("["+hint+"]"), "")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(v) {
		case "":
			return cur, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
	}
}
