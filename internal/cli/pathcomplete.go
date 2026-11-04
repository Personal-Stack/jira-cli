package cli

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/term"

	"github.com/personal-stack/jira-cli/internal/clierr"
)

// certExts are the files offered for a CA file (PEM or DER).
var certExts = []string{".pem", ".crt", ".cer", ".cert", ".der"}

// askPath is ask with Tab completion of files and folders. Completion needs
// a terminal in raw mode; otherwise (pipes, tests) it is a plain ask.
func (p *prompter) askPath(q, cur string, exts []string) (string, error) {
	f, ok := p.in.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return p.ask(q, cur)
	}
	fd := int(f.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return p.ask(q, cur)
	}
	done := make(chan struct{})
	defer close(done)
	go func() { // SIGTERM while waiting: give the terminal back
		select {
		case <-p.ctx.Done():
			_ = term.Restore(fd, old)
		case <-done:
		}
	}()
	defer func() { _ = term.Restore(fd, old) }()

	t := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{f, p.ui.w}, p.ui.question(q, cur))
	if w, h, err := term.GetSize(fd); err == nil && w > 0 {
		_ = t.SetSize(w, h) // wrapping and redraw at the real width, not 80
	}
	t.AutoCompleteCallback = func(line string, pos int, key rune) (string, int, bool) {
		if key != '\t' || pos != len(line) {
			return "", 0, false
		}
		completed, choices := completePath(line, exts)
		if completed == line && len(choices) > 1 {
			_, _ = t.Write([]byte(strings.Join(choices, "  ") + "\r\n"))
		}
		return completed, len(completed), true
	}
	line, err := t.ReadLine()
	if err != nil { // raw mode: Ctrl+C (and Ctrl+D on an empty line) end ReadLine
		return "", clierr.Interrupted()
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

// completePath completes the last element of line as a shell would: the
// longest common prefix of the matching names, with "/" after a folder.
// It also returns the names that match (for listing on an ambiguous Tab).
// Only folders match when exts is nil; else folders and files with these
// extensions, or any file when none has one. Hidden names match only when
// the typed prefix starts with a dot.
func completePath(line string, exts []string) (string, []string) {
	dir, base := "", line
	if i := strings.LastIndexAny(line, "/"+string(os.PathSeparator)); i >= 0 {
		dir, base = line[:i+1], line[i+1:]
	}
	list := dir
	if list == "" {
		list = "."
	} else if list == "~/" || strings.HasPrefix(list, "~/") {
		list = expandHome(list)
	}
	entries, err := os.ReadDir(list)
	if err != nil {
		return line, nil
	}
	var dirs, typed, other []string
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, base) || strings.HasPrefix(n, ".") && !strings.HasPrefix(base, ".") {
			continue
		}
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(list, n)); err == nil {
				isDir = st.IsDir()
			}
		}
		switch {
		case isDir:
			dirs = append(dirs, n+"/")
		case exts == nil:
		case hasExt(n, exts):
			typed = append(typed, n)
		default:
			other = append(other, n)
		}
	}
	if len(typed) == 0 {
		typed = other
	}
	names := append(dirs, typed...)
	sort.Strings(names)
	switch len(names) {
	case 0:
		return line, nil
	case 1:
		return dir + names[0], names
	}
	common := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, common) {
			common = common[:len(common)-1]
		}
	}
	return dir + common, names
}

func hasExt(name string, exts []string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, e := range exts {
		if ext == e {
			return true
		}
	}
	return false
}
