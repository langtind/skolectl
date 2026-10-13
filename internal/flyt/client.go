// Package flyt is the client for the guardian API of Visma Flyt Skole, the
// one behind https://foresatt.visma.no. See reference/README.md for what
// was observed.
package flyt

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

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

// A session.Session has room for two tokens and this login has three parts,
// so the refresh token and the session id are stored together, with a space
// between them. A cookie value holds no space.

func packRefresh(refresh, sessionID string) string { return refresh + " " + sessionID }

// SplitRefresh takes apart what a stored session holds as its refresh token.
func SplitRefresh(packed string) (refresh, sessionID string) {
	refresh, sessionID, _ = strings.Cut(packed, " ")
	return refresh, sessionID
}

// tokenExpiry reads when an access token runs out. A token that cannot be
// read gives the zero time.
func tokenExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}

// SessionFromBrowser reads a login out of the storage state a browser saved
// after the user logged in at foresatt.visma.no, as "playwright-cli
// state-save" writes it.
func SessionFromBrowser(state []byte) (session.Session, error) {
	var saved struct {
		Cookies []struct {
			Name   string `json:"name"`
			Value  string `json:"value"`
			Domain string `json:"domain"`
		} `json:"cookies"`
	}
	if err := json.Unmarshal(state, &saved); err != nil {
		return session.Session{}, ctlkit.Usagef("stdin does not hold a browser storage state: %v", err)
	}
	found := map[string]string{}
	for _, cookie := range saved.Cookies {
		if strings.TrimPrefix(cookie.Domain, ".") == host {
			found[cookie.Name] = cookie.Value
		}
	}
	for _, name := range []string{cookieAccess, cookieRefresh, cookieSession} {
		if found[name] == "" {
			return session.Session{}, ctlkit.Authf("the browser state has no %s cookie for %s", name, host).
				WithHint("log in at https://foresatt.visma.no in that browser first, and save the state while the page is open.")
		}
	}
	return session.Session{
		AccessToken:   found[cookieAccess],
		AccessExpires: tokenExpiry(found[cookieAccess]),
		RefreshToken:  packRefresh(found[cookieRefresh], found[cookieSession]),
	}, nil
}

// cookieCatcher is a cookie jar that keeps what one answer set and sends
// nothing. The client writes its own Cookie header.
type cookieCatcher map[string]string

func (j cookieCatcher) SetCookies(_ *url.URL, cookies []*http.Cookie) {
	for _, cookie := range cookies {
		j[cookie.Name] = cookie.Value
	}
}

func (cookieCatcher) Cookies(*url.URL) []*http.Cookie { return nil }

// RenewSession trades the refresh token for a new access token. The service
// has no request for this alone: any request that carries the refresh token
// and an access token that has run out is answered with new cookies.
func (c *Client) RenewSession(ctx context.Context, packed string) (session.Session, error) {
	refresh, sessionID := SplitRefresh(packed)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/session", nil)
	if err != nil {
		return session.Session{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Requested-By", "XMLHttpRequest")
	addCookies(req, map[string]string{cookieSession: sessionID, cookieRefresh: refresh, cookieAccess: c.Token})

	caught := cookieCatcher{}
	client := *c.HTTP
	client.Jar = caught
	if _, err := httpx.Do(&client, req, service, c.UserAgent); err != nil {
		return session.Session{}, err
	}
	access := caught[cookieAccess]
	if access == "" {
		// The access token it was sent still worked, so none was handed out.
		access = c.Token
	}
	renewed := session.Session{AccessToken: access, AccessExpires: tokenExpiry(access)}
	if caught[cookieRefresh] != "" {
		renewed.RefreshToken = packRefresh(caught[cookieRefresh], sessionID)
	}
	return renewed, nil
}

func addCookies(req *http.Request, cookies map[string]string) {
	for _, name := range []string{cookieSession, cookieAccess, cookieRefresh} {
		if cookies[name] != "" {
			req.AddCookie(&http.Cookie{Name: name, Value: cookies[name]})
		}
	}
}

// formRequest is a body the service takes as a multipart form with the JSON
// in the field "request". Every request that sends a message has this form.
type formRequest struct{ Request any }

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
