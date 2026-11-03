package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestFirstSignalCancelsSecondExits(t *testing.T) {
	sigs := make(chan os.Signal, 2)
	ctx, cancel := context.WithCancel(context.Background())
	exited := make(chan int, 1)
	var out syncBuf
	go handleSignals(sigs, cancel, func(c int) { exited <- c }, &out, 20*time.Millisecond)

	sigs <- os.Interrupt
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("first Ctrl+C did not cancel the context")
	}
	time.Sleep(60 * time.Millisecond)
	if !strings.Contains(out.String(), "press Ctrl+C again") {
		t.Fatalf("no hint while still running: %q", out.String())
	}
	select {
	case c := <-exited:
		t.Fatalf("exited (%d) after one Ctrl+C", c)
	default:
	}

	sigs <- os.Interrupt
	select {
	case c := <-exited:
		if c != 130 {
			t.Fatalf("exit code %d, want 130", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Ctrl+C did not exit")
	}
}

// TestCtrlCAtPromptExits runs the real binary, blocks it at an interactive
// prompt (stdin never answers) and sends SIGINT once: it must exit with 130.
func TestCtrlCAtPromptExits(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := t.TempDir() + "/jira"
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	pr, pw, _ := os.Pipe() // stdin that never sends an answer
	defer pw.Close()
	cmd := exec.Command(bin, "setup-sbx", "-i", "--project", t.TempDir())
	cmd.Stdin = pr
	var stderr syncBuf
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(stderr.String(), "Project the agent works in") {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("no prompt:\n%s", stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	start := time.Now()
	_ = cmd.Process.Signal(syscall.SIGINT)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("one Ctrl+C at a prompt did not end the command")
	}
	if code := cmd.ProcessState.ExitCode(); code != 130 {
		t.Fatalf("exit code %d, want 130\n%s", code, stderr.String())
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
}

// TestSecondCtrlCForcesExit: even when cancelling does not end the command
// (cancel is a no-op here), the second signal exits with 130.
func TestSecondCtrlCForcesExit(t *testing.T) {
	sigs := make(chan os.Signal, 2)
	exited := make(chan int, 1)
	go handleSignals(sigs, func() {}, func(c int) { exited <- c }, &syncBuf{}, time.Hour)
	sigs <- syscall.SIGTERM
	sigs <- os.Interrupt
	select {
	case c := <-exited:
		if c != 130 {
			t.Fatalf("exit %d", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no exit on the second signal")
	}
}
