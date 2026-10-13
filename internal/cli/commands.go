package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/langtind/ctlkit"
	"github.com/langtind/ctlkit/session"

	"github.com/langtind/skolectl/internal/flyt"
)

const (
	defaultLimit = 20
	// maxLimit is the most threads or messages one command prints. The
	// service has no paging and answers with everything.
	maxLimit = 500
)

var outputFlag = ctlkit.Flag{Name: "output", Kind: ctlkit.String, Value: "path", Help: "where to save the file, instead of under its own name in the current directory"}

// commands is the command table, the single description of the command
// line. Add a command or a flag here and nowhere else.
func (a *App) commands() []ctlkit.Command {
	return []ctlkit.Command{
		{
			Name:    "login",
			Summary: "Store the login a person made in a browser, read from stdin",
			Flags: []ctlkit.Flag{
				{Name: "state-stdin", Kind: ctlkit.Bool, Help: "read the browser storage state from stdin"},
			},
			Run: a.login,
		},
		{
			Name:    "logout",
			Summary: "Remove the stored login",
			Run:     a.logout,
		},
		{
			Name:    "status",
			Summary: "Show whether a login is stored and how long it lasts, without calling Visma Flyt Skole",
			Flags:   []ctlkit.Flag{ctlkit.JSONFlag},
			Run:     a.status,
		},
		{
			Name:    "refresh",
			Summary: "Renew the stored login now",
			Flags:   []ctlkit.Flag{ctlkit.JSONFlag},
			Run:     a.refresh,
		},
		{
			Name:    "overview",
			Summary: "Count what waits for the guardian, for each child: unread messages and notices, unanswered forms and consents, open applications",
			Flags:   []ctlkit.Flag{childFlag, ctlkit.JSONFlag, ctlkit.FieldsFlag},
			Run:     a.overview,
		},
		{
			Name:    "children",
			Summary: "List the children, with the pupil id of each",
			Flags:   []ctlkit.Flag{ctlkit.JSONFlag, ctlkit.FieldsFlag},
			Run:     a.children,
		},
		{
			Name:    "threads",
			Summary: "List the message threads of every child, latest first",
			Flags: []ctlkit.Flag{
				childFlag,
				{Name: "kind", Kind: ctlkit.String, Value: "kind", Enum: []string{kindDirect, kindGroup}, Help: "only direct threads with one employee, or only group conversations"},
				{Name: "unread", Kind: ctlkit.Bool, Help: "only threads with unread messages"},
				ctlkit.LimitFlag(defaultLimit, maxLimit), ctlkit.JSONFlag, ctlkit.FieldsFlag,
			},
			Run: a.threads,
		},
		{
			Name:    "thread",
			Summary: "Show the latest messages of one thread, oldest first, without marking it as read",
			Args:    []ctlkit.Arg{{Name: "thread-id", Required: true, ID: true, Help: "the id, as threads prints it"}},
			Flags:   []ctlkit.Flag{ctlkit.LimitFlag(defaultLimit, maxLimit), ctlkit.JSONFlag, ctlkit.FieldsFlag},
			Run:     a.thread,
		},
		{
			Name:    "reply",
			Summary: "Send a message in a thread. Running it again sends one more",
			Mutates: true,
			Args: []ctlkit.Arg{
				{Name: "thread-id", Required: true, ID: true, Help: "the id, as threads prints it"},
				{Name: "text", Required: true, Help: "the message"},
			},
			Flags: []ctlkit.Flag{ctlkit.DryRunFlag, ctlkit.YesFlag, ctlkit.JSONFlag},
			Run:   a.reply,
		},
		{
			Name:    "mark-read",
			Summary: "Mark a thread as read, which the school can see. Running it again changes nothing",
			Mutates: true,
			Args:    []ctlkit.Arg{{Name: "thread-id", Required: true, ID: true, Help: "the id, as threads prints it"}},
			Flags:   []ctlkit.Flag{ctlkit.DryRunFlag, ctlkit.YesFlag, ctlkit.JSONFlag},
			Run:     a.markRead,
		},
		{
			Name:    "attachment",
			Summary: "Save a file from a group conversation",
			Args: []ctlkit.Arg{
				{Name: "thread-id", Required: true, ID: true, Help: "the id, as threads prints it"},
				{Name: "attachment-id", Required: true, ID: true, Help: "the id, as thread prints it"},
			},
			Flags: []ctlkit.Flag{outputFlag, ctlkit.JSONFlag},
			Run:   a.attachment,
		},
		{
			Name:    "notices",
			Summary: "List the one-way notices from the school, latest first",
			Flags: []ctlkit.Flag{
				childFlag,
				{Name: "kind", Kind: ctlkit.String, Value: "kind", Enum: []string{noticeNotification, noticeAnnouncement}, Help: "only notifications, or only announcements"},
				{Name: "unread", Kind: ctlkit.Bool, Help: "only unread notices"},
				ctlkit.LimitFlag(defaultLimit, maxLimit), ctlkit.JSONFlag, ctlkit.FieldsFlag,
			},
			Run: a.notices,
		},
		{
			Name:    "absences",
			Summary: "Show the absence of each child in the current school year",
			Flags: []ctlkit.Flag{
				childFlag,
				{Name: "term", Kind: ctlkit.String, Value: "term", Enum: []string{"fall", "spring"}, Help: "only one term"},
				ctlkit.JSONFlag, ctlkit.FieldsFlag,
			},
			Run: a.absences,
		},
		{
			Name:    "forms",
			Summary: "List the forms the school asks the guardian to answer",
			Flags: []ctlkit.Flag{
				childFlag,
				{Name: "unanswered", Kind: ctlkit.Bool, Help: "only forms that are not answered and still open"},
				ctlkit.JSONFlag, ctlkit.FieldsFlag,
			},
			Run: a.forms,
		},
		{
			Name:    "form",
			Summary: "Show one form with its alternatives and what was answered",
			Args:    []ctlkit.Arg{{Name: "form-id", Required: true, ID: true, Help: "the id, as forms prints it"}},
			Flags:   []ctlkit.Flag{ctlkit.JSONFlag, ctlkit.FieldsFlag},
			Run:     a.form,
		},
		{
			Name:    "consents",
			Summary: "List what the school asks consent to, and what was answered",
			Flags:   []ctlkit.Flag{childFlag, ctlkit.JSONFlag, ctlkit.FieldsFlag},
			Run:     a.consents,
		},
		{
			Name:    "applications",
			Summary: "List the applications sent to the school, such as for leave, latest first",
			Flags:   []ctlkit.Flag{childFlag, ctlkit.JSONFlag, ctlkit.FieldsFlag},
			Run:     a.applications,
		},
		{
			Name:    "application",
			Summary: "Show one application with its dates, its text and the answer",
			Args:    []ctlkit.Arg{{Name: "application-id", Required: true, ID: true, Help: "the id, as applications prints it"}},
			Flags:   []ctlkit.Flag{ctlkit.JSONFlag, ctlkit.FieldsFlag},
			Run:     a.application,
		},
		{
			Name:    "classes",
			Summary: "Show the class of each child, the contact teacher and who teaches each subject",
			Flags:   []ctlkit.Flag{childFlag, ctlkit.JSONFlag, ctlkit.FieldsFlag},
			Run:     a.classes,
		},
		{
			Name:    "schools",
			Summary: "Show how to reach each school, and the links it lists",
			Flags:   []ctlkit.Flag{childFlag, ctlkit.JSONFlag, ctlkit.FieldsFlag},
			Run:     a.schools,
		},
		{
			Name:    "documents",
			Summary: "List the files about each child and the files the school shares",
			Flags:   []ctlkit.Flag{childFlag, ctlkit.JSONFlag, ctlkit.FieldsFlag},
			Run:     a.documents,
		},
		{
			Name:    "document",
			Summary: "Save one file",
			Args:    []ctlkit.Arg{{Name: "document-id", Required: true, ID: true, Help: "the id, as documents prints it"}},
			Flags:   []ctlkit.Flag{outputFlag, ctlkit.JSONFlag},
			Run:     a.document,
		},
	}
}

func (a *App) login(ctx context.Context, c *ctlkit.Call) error {
	if !c.Bool("state-stdin") || c.Interactive() {
		return ctlkit.Usagef("login takes a browser storage state on stdin, with --state-stdin").
			WithHint("%s", loginSteps)
	}
	state, err := io.ReadAll(io.LimitReader(c.Program.Stdin, 16<<20))
	if err != nil {
		return fmt.Errorf("read the browser state: %w", err)
	}
	login, err := flyt.SessionFromBrowser(state)
	if err != nil {
		return err
	}

	// Renewing at once proves that the tool can keep the login alive on
	// its own, and names who it belongs to.
	a.Client.Token = login.AccessToken
	_, a.Client.SessionID = flyt.SplitRefresh(login.RefreshToken)
	renewed, err := a.Client.RenewSession(ctx, login.RefreshToken)
	if err != nil {
		if ctlkit.KindOf(err) == ctlkit.KindAuth {
			return ctlkit.Authf("Visma Flyt Skole refused the login in the browser state: %w", err).
				WithHint("the login may have run out. Log in again in the browser and save the state straight away.")
		}
		return err
	}
	if renewed.RefreshToken == "" {
		renewed.RefreshToken = login.RefreshToken
	}
	a.Client.Token = renewed.AccessToken
	guardian, err := a.Client.Me(ctx)
	if err != nil {
		return err
	}
	if err := a.Session.Save(renewed); err != nil {
		return fmt.Errorf("store the login in the keyring: %w", err)
	}
	fmt.Fprintf(c.Stdout, "Logged in as %s at %s. The login is in the system keyring and renews itself.\n",
		oneLine(guardian.FirstName+" "+guardian.LastName), oneLine(guardian.Municipality.Name))
	return nil
}

func (a *App) logout(ctx context.Context, c *ctlkit.Call) error {
	if err := a.Session.Clear(); err != nil {
		return fmt.Errorf("remove the login from the keyring: %w", err)
	}
	fmt.Fprintln(c.Stdout, "Removed the stored login.")
	return nil
}

// loginStatus is what status and refresh print.
type loginStatus struct {
	LoggedIn bool `json:"loggedIn"`
	// AccessExpires is when the access token runs out. It lasts five
	// minutes and is renewed when a command needs it.
	AccessExpires string `json:"accessExpires,omitempty"`
	// Renewable says that a refresh token is stored. Visma Flyt Skole does
	// not say how long it lasts.
	Renewable bool `json:"renewable"`
}

func (a *App) loginStatus() (loginStatus, error) {
	login, err := a.Session.Load()
	if errors.Is(err, session.ErrNotLoggedIn) {
		return loginStatus{}, nil
	}
	if err != nil {
		return loginStatus{}, err
	}
	refresh, _ := flyt.SplitRefresh(login.RefreshToken)
	status := loginStatus{Renewable: refresh != ""}
	if !login.AccessExpires.IsZero() {
		status.AccessExpires = login.AccessExpires.UTC().Format(time.RFC3339)
	}
	status.LoggedIn = status.Renewable || a.Now().Before(login.AccessExpires)
	return status, nil
}

func (a *App) status(ctx context.Context, c *ctlkit.Call) error {
	status, err := a.loginStatus()
	if err != nil {
		return err
	}
	return c.Print(status, func(w io.Writer) { a.renderStatus(w, status) })
}

func (a *App) refresh(ctx context.Context, c *ctlkit.Call) error {
	login, err := a.Session.Load()
	if err != nil {
		return loginError(err)
	}
	_, a.Client.SessionID = flyt.SplitRefresh(login.RefreshToken)
	if _, err := a.Session.Refresh(ctx, login.AccessToken); err != nil {
		return loginError(err)
	}
	return a.status(ctx, c)
}
