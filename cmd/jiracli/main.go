// Command jiracli is the spec-driven Jira Data Center CLI.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/personal-stack/jira-cli/generated"
	"github.com/personal-stack/jira-cli/internal/cli"
	"github.com/personal-stack/jira-cli/internal/clierr"
)

// version is set at build time: -ldflags "-X main.version=..."
var version = "dev"

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go handleSignals(sigs, cancel, os.Exit, os.Stderr, 500*time.Millisecond)

	app := cli.NewApp(version, generated.Profiles, generated.DefaultProfile)
	code := app.Execute(ctx, os.Args[1:])
	signal.Stop(sigs)
	os.Exit(code)
}

// handleSignals implements "Ctrl+C twice exits":
//   - the first signal cancels ctx, so requests, child processes and prompts
//     stop and the command exits on its own (code 130); if it is still running
//     after hintAfter, a hint says how to force it;
//   - the second signal exits immediately with code 130.
func handleSignals(sigs <-chan os.Signal, cancel context.CancelFunc, exit func(int), w io.Writer, hintAfter time.Duration) {
	<-sigs
	cancel()
	hint := time.NewTimer(hintAfter)
	defer hint.Stop()
	for {
		select {
		case <-hint.C:
			fmt.Fprintln(w, "\nStopping... press Ctrl+C again to exit immediately.")
		case <-sigs:
			fmt.Fprintln(w, "\nInterrupted.")
			exit(clierr.ExitInterrupted)
			return
		}
	}
}
