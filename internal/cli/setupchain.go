package cli

import (
	"context"
	"fmt"

	"github.com/personal-stack/jira-cli/internal/clierr"
	"github.com/personal-stack/jira-cli/internal/config"
	"github.com/personal-stack/jira-cli/internal/credentials"
	"github.com/personal-stack/jira-cli/internal/transport"
)

// finishSetup runs after `config setup` wrote an instance, in a guided run:
// it offers to store the token and to check the connection, so one session
// goes from nothing to a working CLI.
func (a *App) finishSetup(ctx context.Context, ui *termUI, name string) error {
	a.instance, a.cfgLoaded = name, false
	in, _, err := a.Instance()
	if err != nil {
		return err
	}
	q := a.form(ctx, ui)
	// The follow-ups are optional: the configuration is written. End of
	// input stops here with the remaining steps; Ctrl+C is still an error.
	stop := func(err error) error {
		if clierr.As(err).Code == "no_answer" {
			ui.note("Later: " + ui.cmd("jiracli auth login") + ", then " + ui.cmd("jiracli doctor"))
			return nil
		}
		return err
	}

	ui.section("Token")
	switch present, source := a.credentialPresent(ctx, in); {
	case present:
		ui.ok("token " + in.Auth.CredentialRef + " is available (" + source + ")")
	case in.Auth.Type != "pat":
		ui.note("OAuth: authorize with " + ui.cmd("jiracli auth login") + " when you are ready.")
	default:
		store := true
		q.yesno(&store, "Store the token now (a personal access token from your Jira profile)")
		if q.err != nil {
			return stop(q.err)
		}
		if !store {
			ui.note("Later: " + ui.cmd("jiracli auth login"))
		} else if err := a.askAndStorePAT(ctx, ui, q, in); err != nil {
			return stop(err)
		}
	}

	ui.section("Connection")
	check := true
	q.yesno(&check, "Check the connection now (jiracli doctor)")
	if q.err != nil {
		return stop(q.err)
	}
	if !check {
		ui.note("Later: " + ui.cmd("jiracli doctor"))
		return nil
	}
	checks, failed := a.runDoctor(ctx)
	for _, c := range checks {
		switch c.Status {
		case "ok":
			ui.ok(c.Name)
		case "skip":
			ui.note("- " + c.Name + ": " + c.Detail)
		case "warn":
			ui.warn(c.Name + ": " + c.Detail)
		default:
			ui.bad(c.Name + ": " + c.Detail)
		}
	}
	if failed != nil {
		return clierr.New(failed.Layer, "doctor_failed", "the configuration was written, but check "+failed.Name+" failed: "+failed.Detail).
			WithHint(or(failed.Hint, "fix it, then run jiracli doctor"))
	}
	ui.section("Ready")
	ui.println("  Try: " + ui.cmd("jiracli myself get"))
	return nil
}

// credentialPresent reports whether the instance's credential is available
// (keyring, file or environment), without network access.
func (a *App) credentialPresent(ctx context.Context, in *config.Instance) (bool, string) {
	hc, err := transport.New(in, a.timeout)
	if err != nil {
		return false, ""
	}
	ap, err := a.AuthProvider(in, hc)
	if err != nil {
		return false, ""
	}
	st := ap.Status(ctx)
	return st.Present, st.Source
}

// askAndStorePAT asks for the token (hidden) until Jira accepts it, at most
// three times, and stores it.
func (a *App) askAndStorePAT(ctx context.Context, ui *termUI, q *form, in *config.Instance) error {
	var last error
	for try := 0; try < 3; try++ {
		tok, err := q.p.secret("Personal access token (input hidden)")
		if err != nil {
			return err
		}
		res, err := a.storePAT(ctx, in, credentials.NewSecret([]byte(tok)), false)
		if err == nil {
			who := ""
			if u, ok := res["user"].(map[string]any); ok {
				name, _ := u["name"].(string)
				display, _ := u["displayName"].(string)
				who = " as " + or(display, name)
			}
			ui.ok(fmt.Sprintf("token verified%s and stored (%v)", who, res["store"]))
			return nil
		}
		last = err
		e := clierr.As(err)
		ui.warn(e.Error())
		if e.Hint != "" {
			ui.note(e.Hint)
		}
	}
	return last
}

// offerLogin runs after a command failed for lack of a stored token: in a
// terminal it offers to store it now. It reports whether it was stored.
func (a *App) offerLogin(ctx context.Context) bool {
	in, _, err := a.Instance()
	if err != nil || in.Auth.Type != "pat" {
		return false
	}
	ui := a.newTermUI()
	q := a.form(ctx, ui)
	store := true
	ui.println("")
	q.yesno(&store, "No token stored for instance "+in.Name+". Store it now")
	if q.err != nil || !store {
		return false
	}
	if err := a.askAndStorePAT(ctx, ui, q, in); err != nil {
		return false
	}
	ui.note("Token stored; run your command again.")
	return true
}
