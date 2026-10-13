// Package flyt is the client for the guardian API of Visma Flyt Skole, the
// one behind https://foresatt.visma.no. See reference/README.md for what
// was observed.
package flyt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"

	"github.com/langtind/ctlkit"
	"github.com/langtind/ctlkit/httpx"
	"github.com/langtind/ctlkit/session"
)

const (
	service = "Visma Flyt Skole"

	host = "api.foresatt.visma.no"

	// The login is three cookies. The access token is a JWT that lasts five
	// minutes. The session id and the refresh token last for the whole login.
	cookieAccess  = "CookieAccessToken"
	cookieRefresh = "CookieRefreshToken"
	cookieSession = "GwebId"
)

type Client struct {
	HTTP      *http.Client
	BaseURL   string
	UserAgent string
	// Token is the access token and SessionID the session id. A request
	// needs both.
	Token     string
	SessionID string
	// Renew returns a new access token after the service refused Token with
	// a 401. The request is then sent once more. Nil means a 401 is final.
	Renew func(ctx context.Context) (string, error)
}

func NewClient() *Client {
	return &Client{HTTP: httpx.NewClient(), BaseURL: "https://" + host}
}

// ExtraSessionID is where a stored session keeps the session id, in Extra.
const ExtraSessionID = "sessionId"

// SessionFromBrowser reads a login out of the storage state a browser saved
// after the user logged in at foresatt.visma.no, as "playwright-cli
// state-save" writes it.
func SessionFromBrowser(state []byte) (session.Session, error) {
	found, err := httpx.BrowserCookies(state, host)
	if err != nil {
		return session.Session{}, err
	}
	for _, name := range []string{cookieAccess, cookieRefresh, cookieSession} {
		if found[name] == "" {
			return session.Session{}, ctlkit.Authf("the browser state has no %s cookie for %s", name, host).
				WithHint("log in at https://foresatt.visma.no in that browser first, and save the state while the page is open.")
		}
	}
	return session.Session{
		AccessToken:   found[cookieAccess],
		AccessExpires: session.JWTExpiry(found[cookieAccess]),
		RefreshToken:  found[cookieRefresh],
		Extra:         map[string]string{ExtraSessionID: found[cookieSession]},
	}, nil
}

// RenewSession trades the refresh token for a new access token. The service
// has no request for this alone: any request that carries the session id and
// the refresh token is answered with new cookies. The access token is left
// out: sent one that still works, the service hands out no new one.
func (c *Client) RenewSession(ctx context.Context, stored session.Session) (session.Session, error) {
	sessionID := stored.Extra[ExtraSessionID]
	if sessionID == "" {
		return session.Session{}, ctlkit.Authf("the stored login has no session id")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/session", nil)
	if err != nil {
		return session.Session{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Requested-By", "XMLHttpRequest")
	addCookies(req, map[string]string{cookieSession: sessionID, cookieRefresh: stored.RefreshToken})

	caught := httpx.CookieCatcher{}
	client := *c.HTTP
	client.Jar = caught
	if _, err := httpx.Do(&client, req, service, c.UserAgent); err != nil {
		return session.Session{}, err
	}
	access := caught[cookieAccess]
	if access == "" {
		return session.Session{}, fmt.Errorf("%s: the answer set no %s cookie", service, cookieAccess)
	}
	return session.Session{
		AccessToken:   access,
		AccessExpires: session.JWTExpiry(access),
		RefreshToken:  caught[cookieRefresh],
	}, nil
}

func addCookies(req *http.Request, cookies map[string]string) {
	for _, name := range []string{cookieSession, cookieAccess, cookieRefresh} {
		if cookies[name] != "" {
			req.AddCookie(&http.Cookie{Name: name, Value: cookies[name]})
		}
	}
}

// formRequest is a body the service takes as a multipart form with the JSON
// in the field "request", and any files in the field "attachments". Every
// request that sends a message or an application has this form.
type formRequest struct {
	Request any
	Files   []FormFile
}

// do sends one request with the login and decodes a 2xx answer into into.
// Any other status comes back as an error with a kind, see httpx.Do. When
// the service refuses the access token, the session is renewed and the
// request is sent once more.
func (c *Client) do(ctx context.Context, method, path string, body, into any) error {
	answer, err := c.fetch(ctx, method, path, body)
	if err != nil {
		return err
	}
	if into == nil || len(answer) == 0 {
		return nil
	}
	if err := json.Unmarshal(answer, into); err != nil {
		return fmt.Errorf("%s: read the answer to %s %s: %w", service, method, path, err)
	}
	return nil
}

// fetch is do without the decoding. A file is fetched with it.
func (c *Client) fetch(ctx context.Context, method, path string, body any) ([]byte, error) {
	answer, err := c.send(ctx, method, path, body)
	if httpx.Status(err) != http.StatusUnauthorized || c.Renew == nil {
		return answer, err
	}
	token, renewErr := c.Renew(ctx)
	if renewErr != nil {
		return nil, renewErr
	}
	c.Token = token
	return c.send(ctx, method, path, body)
}

func (c *Client) send(ctx context.Context, method, path string, body any) ([]byte, error) {
	payload := &bytes.Buffer{}
	contentType := ""
	switch body := body.(type) {
	case nil:
	case formRequest:
		data, err := json.Marshal(body.Request)
		if err != nil {
			return nil, err
		}
		form := multipart.NewWriter(payload)
		if err := form.WriteField("request", string(data)); err != nil {
			return nil, err
		}
		for _, file := range body.Files {
			header := textproto.MIMEHeader{}
			header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "attachments", "filename": file.Name}))
			header.Set("Content-Type", file.ContentType)
			part, err := form.CreatePart(header)
			if err != nil {
				return nil, err
			}
			if _, err := part.Write(file.Data); err != nil {
				return nil, err
			}
		}
		if err := form.Close(); err != nil {
			return nil, err
		}
		contentType = form.FormDataContentType()
	default:
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload.Write(data)
		contentType = "application/json"
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/"+path, payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Requested-By", "XMLHttpRequest")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	addCookies(req, map[string]string{cookieSession: c.SessionID, cookieAccess: c.Token})
	return httpx.Do(c.HTTP, req, service, c.UserAgent)
}

// Guardian is who is logged in.
type Guardian struct {
	FirstName    string `json:"firstName"`
	LastName     string `json:"lastName"`
	Municipality struct {
		Name        string `json:"name"`
		CustomerKey string `json:"customerKey"`
	} `json:"municipality"`
}

// Me returns who is logged in.
func (c *Client) Me(ctx context.Context) (*Guardian, error) {
	var guardian Guardian
	err := c.do(ctx, http.MethodGet, "v1/session", nil, &guardian)
	return &guardian, err
}
