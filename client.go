package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Client is a qBittorrent Web API v2 session. It logs in at most once per
// process: the first call logs in, and a failed login is remembered and
// returned by every later call, never retried, because qBittorrent bans an
// IP after repeated failures.
type Client struct {
	Base, User, Pass string
	HTTP             *http.Client

	mu       sync.Mutex // login is shared by concurrent calls (tracker summary's pool)
	tried    bool
	loginErr error
}

// ErrNoCredentials is any API call's error while no password is configured;
// nothing is sent.
var ErrNoCredentials = errors.New("no credentials: set the password with o qbt config set pass (or $O_QBT_PASS)")

// ErrLoginRefused is a login qBittorrent answered with anything but Ok.
var ErrLoginRefused = errors.New("login refused: check user/pass; qBittorrent bans an IP after repeated failures, so do not retry blindly")

// timeout is each request's deadline.
const timeout = 15 * time.Second

func newClient(c Config) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{Base: c.URL, User: c.User, Pass: c.Pass, HTTP: &http.Client{Jar: jar, Timeout: timeout}}
}

// login posts the credentials once; its outcome is kept for the process.
func (c *Client) login(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tried {
		return c.loginErr
	}
	c.tried = true
	if c.User == "" || c.Pass == "" {
		c.loginErr = ErrNoCredentials
		return c.loginErr
	}
	form := url.Values{"username": {c.User}, "password": {c.Pass}}
	status, body, err := c.send(ctx, http.MethodPost, "/api/v2/auth/login", form)
	switch {
	case err != nil:
		c.loginErr = err
	case status == http.StatusForbidden:
		c.loginErr = fmt.Errorf("%w (HTTP 403: this IP may already be banned; wait for the ban to lapse or clear it in the web UI)", ErrLoginRefused)
	case status != http.StatusOK || strings.TrimSpace(string(body)) != "Ok.":
		c.loginErr = fmt.Errorf("%w (HTTP %d: %s)", ErrLoginRefused, status, oneLine(string(body)))
	}
	return c.loginErr
}

// send is one request with qBittorrent's Referer; a timeout or an
// unreachable host is a plain error naming the URL.
func (c *Client) send(ctx context.Context, method, path string, form url.Values) (int, []byte, error) {
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader(form.Encode())
	} else if len(form) > 0 {
		path += "?" + form.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Referer", c.Base)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
			return 0, nil, fmt.Errorf("qBittorrent at %s did not answer within %s (timeout): is the NAS up and the URL right?", c.Base, c.HTTP.Timeout)
		}
		return 0, nil, fmt.Errorf("qBittorrent at %s: %w", c.Base, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	return res.StatusCode, data, err
}

// HTTPError is an API answer other than 200.
type HTTPError struct {
	Path   string
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s: HTTP %d %s", e.Path, e.Status, oneLine(e.Body))
}

// call logs in (once) and makes one API request; non-200 is an HTTPError.
func (c *Client) call(ctx context.Context, method, path string, form url.Values) ([]byte, error) {
	if err := c.login(ctx); err != nil {
		return nil, err
	}
	status, body, err := c.send(ctx, method, path, form)
	if err != nil {
		return nil, err
	}
	if status == http.StatusForbidden {
		return nil, fmt.Errorf("%s: HTTP 403 after login (session refused); not retrying", path)
	}
	if status != http.StatusOK {
		return nil, &HTTPError{Path: path, Status: status, Body: string(body)}
	}
	return body, nil
}

// Get is a GET of path decoded into out (a *string takes the body as text).
func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	body, err := c.call(ctx, http.MethodGet, path, query)
	if err != nil {
		return err
	}
	if s, ok := out.(*string); ok {
		*s = strings.TrimSpace(string(body))
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// Post is a POST of form to path.
func (c *Client) Post(ctx context.Context, path string, form url.Values) error {
	_, err := c.call(ctx, http.MethodPost, path, form)
	return err
}

// PostEither posts to the v5 path, falling back to the v4 path when the
// server does not know it (404): torrents/stop then torrents/pause,
// torrents/start then torrents/resume. It answers the path that worked.
func (c *Client) PostEither(ctx context.Context, v5, v4 string, form url.Values) (string, error) {
	err := c.Post(ctx, v5, form)
	var he *HTTPError
	if errors.As(err, &he) && he.Status == http.StatusNotFound {
		return v4, c.Post(ctx, v4, form)
	}
	return v5, err
}

// Pause stops hashes ("all" for every torrent).
func (c *Client) Pause(ctx context.Context, hashes string) (string, error) {
	return c.PostEither(ctx, "/api/v2/torrents/stop", "/api/v2/torrents/pause", url.Values{"hashes": {hashes}})
}

// Resume starts hashes ("all" for every torrent).
func (c *Client) Resume(ctx context.Context, hashes string) (string, error) {
	return c.PostEither(ctx, "/api/v2/torrents/start", "/api/v2/torrents/resume", url.Values{"hashes": {hashes}})
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
