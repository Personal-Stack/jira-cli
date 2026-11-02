// Command jira is the spec-driven Jira Data Center CLI.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/personal-stack/jira-cli/generated"
	"github.com/personal-stack/jira-cli/internal/cli"
)

// version is set at build time: -ldflags "-X main.version=..."
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	app := cli.NewApp(version, generated.Profiles, generated.DefaultProfile)
	code := app.Execute(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
