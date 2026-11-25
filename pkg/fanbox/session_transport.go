package fanbox

import (
	"context"
	"errors"
	"fmt"
	"github.com/hashicorp/go-retryablehttp"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var ErrInvalidSession = errors.New("FANBOX session is no longer valid; stopping requests")

// sessionTransport serializes FANBOX request headers, including retry attempts.
// The outer net/http client owns the cookie jar and redirect policy.
type sessionTransport struct {
	base   http.RoundTripper
	client *OfficialAPIClient
	mu     sync.Mutex
	err    error
}

func isFanboxURL(u *url.URL) bool {
	host := u.Hostname()
	return host == "fanbox.cc" || strings.HasSuffix(host, ".fanbox.cc")
}

func (c *OfficialAPIClient) initSession() {
	hc := c.HTTPClient.HTTPClient
	if hc.Jar == nil {
		hc.Jar = NewCookieJar()
	}
	apiURL := &url.URL{Scheme: "https", Host: "api.fanbox.cc", Path: "/"}
	seed := &http.Request{Header: http.Header{"Cookie": []string{c.Cookie}}}
	cookies := seed.Cookies()
	for _, cookie := range cookies {
		cookie.Path = "/"
		cookie.Secure = true
	}
	hc.Jar.SetCookies(apiURL, cookies)
	base := hc.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	c.transport = &sessionTransport{base: base, client: c}
	previousRetry := c.HTTPClient.CheckRetry
	c.HTTPClient.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		if failure := c.transport.failure(); failure != nil {
			return false, failure
		}
		if errors.Is(err, ErrInvalidSession) || errors.Is(err, ErrStatusForbidden) {
			return false, err
		}
		if previousRetry != nil {
			return previousRetry(ctx, resp, err)
		}
		return retryablehttp.DefaultRetryPolicy(ctx, resp, err)
	}
	previousPrepare := c.HTTPClient.PrepareRetry
	c.HTTPClient.PrepareRetry = func(req *http.Request) error {
		// net/http adds jar cookies to the request; remove that snapshot on retry.
		if isFanboxURL(req.URL) {
			req.Header.Del("Cookie")
		}
		if previousPrepare != nil {
			return previousPrepare(req)
		}
		return nil
	}
	hc.Transport = c.transport
	previousRedirect := hc.CheckRedirect
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// API endpoints return JSON, never login pages or cross-origin redirects.
		if len(via) > 0 && via[0].URL.Hostname() == "api.fanbox.cc" {
			c.transport.stop(ErrInvalidSession)
			return http.ErrUseLastResponse
		}
		if previousRedirect != nil {
			return previousRedirect(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
}

func (t *sessionTransport) failure() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

func (t *sessionTransport) stop(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.err == nil {
		t.err = err
	}
}

func (t *sessionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !isFanboxURL(req.URL) {
		return t.base.RoundTrip(req)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.err != nil {
		return nil, t.err
	}
	t.client.rateLimiterMu.RLock()
	limiter := t.client.rateLimiter
	t.client.rateLimiterMu.RUnlock()
	if limiter != nil {
		if err := limiter.Wait(req.Context()); err != nil {
			return nil, err
		}
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		t.err = ErrInvalidSession
	case http.StatusForbidden:
		t.err = ErrStatusForbidden
	}
	if req.URL.Hostname() == "api.fanbox.cc" {
		for _, cookie := range resp.Cookies() {
			if cookie.Name == "FANBOXSESSID" && (cookie.Value == "" || cookie.MaxAge < 0 || (!cookie.Expires.IsZero() && cookie.Expires.Before(time.Now()))) {
				t.err = ErrInvalidSession
				break
			}
		}
	}
	return resp, nil
}

// ValidateSession checks an authenticated API request without requiring a paid plan.
func (c *OfficialAPIClient) ValidateSession(ctx context.Context) error {
	c.initOnce.Do(c.initSession)
	apiURL := &url.URL{Scheme: "https", Host: "api.fanbox.cc", Path: "/"}
	hasSession := false
	for _, cookie := range c.HTTPClient.HTTPClient.Jar.Cookies(apiURL) {
		if cookie.Name == "FANBOXSESSID" && cookie.Value != "" {
			hasSession = true
			break
		}
	}
	if !hasSession {
		return ErrInvalidSession
	}
	var plans PlanListSupportingResponse
	return c.RequestAndUnwrapJSON(ctx, http.MethodGet, "https://api.fanbox.cc/plan.listSupporting", &plans)
}
