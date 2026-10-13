// Package cli implements the skolectl command line.
package cli

import (
	"context"
	"errors"
	"io/fs"
	"time"

	"github.com/langtind/ctlkit"
	"github.com/langtind/ctlkit/keystore"
	"github.com/langtind/ctlkit/session"

	"github.com/langtind/skolectl/internal/flyt"
)

const (
	// Name is the program name. A rename also touches the module path, the
	// skill directory with its embed line in skill_embed.go, and the docs.
	Name = "skolectl"

	summary = "read and answer what a school sends guardians through Visma Flyt Skole"

	// loginSteps is the hint of every error that only a new login mends.
	// ID-porten needs a person, so the login is made in a browser and
	// handed over.
	loginSteps = `a person has to log in with ID-porten in a browser. Run "playwright-cli open https://foresatt.visma.no/<school-key> --headed", let the person log in, then "playwright-cli state-save state.json", then "` + Name + ` login --state-stdin < state.json", and delete state.json.`

	// renewMargin is how long before the access token runs out it is
	// renewed. The token lasts five minutes, so the margin has to be short.
	renewMargin = 30 * time.Second
)

// App holds what the commands work with. Tests replace the fields to point
// skolectl at a fake service and at a keyring in memory.
type App struct {
	Client *flyt.Client
	// Session holds the login and renews it.
	Session *session.Manager
	// Now is the clock, and Zone the time zone that times are shown in.
	Now  func() time.Time
	Zone *time.Location
}

// New returns skolectl as it runs for real.
func New(build ctlkit.Build, skill fs.FS) *ctlkit.Program {
	app := &App{
		Client:  flyt.NewClient(),
		Session: &session.Manager{Store: keystore.Keyring(Name, "token"), LockPath: session.LockFile(Name)},
		Now:     time.Now,
		Zone:    time.Local,
	}
	return app.program(build, skill)
}

func (a *App) program(build ctlkit.Build, skill fs.FS) *ctlkit.Program {
	p := ctlkit.New(Name, summary, build)
	p.URL = "https://github.com/langtind/" + Name
	p.Skill = skill
	p.Commands = a.commands()
	p.Hint = hint
	a.Client.UserAgent = p.UserAgent()
	a.Session.Renew = a.Client.RenewSession
	a.Session.Now = a.Now
	a.Session.Margin = renewMargin
	return p
}

// hint says what to do about an error that cannot say it itself.
func hint(err error) string {
	if ctlkit.KindOf(err) == ctlkit.KindAuth {
		return loginSteps
	}
	return ""
}

// loginError adds what to do to an error from the session.
func loginError(err error) error {
	if errors.Is(err, session.ErrNotLoggedIn) || errors.Is(err, session.ErrExpired) {
		return ctlkit.Authf("%w", err).WithHint("%s", loginSteps)
	}
	return err
}

// authorize gives the client the stored login, renewed when the access
// token is about to run out. The login is also renewed when Visma Flyt Skole
// refuses the token in the middle of a run.
func (a *App) authorize(ctx context.Context) error {
	login, err := a.Session.Load()
	if err != nil {
		return loginError(err)
	}
	_, a.Client.SessionID = flyt.SplitRefresh(login.RefreshToken)
	a.Client.Token = login.AccessToken
	token, err := a.Session.Token(ctx)
	if err != nil {
		return loginError(err)
	}
	a.Client.Token = token
	a.Client.Renew = func(ctx context.Context) (string, error) {
		token, err := a.Session.Refresh(ctx, a.Client.Token)
		return token, loginError(err)
	}
	return nil
}
