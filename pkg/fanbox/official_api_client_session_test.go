package fanbox

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/stretchr/testify/require"
)

type sessionTestTransport func(*http.Request) (*http.Response, error)

func (f sessionTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func sessionResponse(req *http.Request, status int, body string, cookies ...string) *http.Response {
	header := make(http.Header)
	for _, cookie := range cookies {
		header.Add("Set-Cookie", cookie)
	}
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: req}
}

func sessionClient(transport sessionTestTransport) *OfficialAPIClient {
	hc := retryablehttp.NewClient()
	hc.Logger = nil
	hc.RetryMax = 2
	hc.RetryWaitMin = time.Millisecond
	hc.RetryWaitMax = time.Millisecond
	hc.HTTPClient.Transport = transport
	return &OfficialAPIClient{HTTPClient: hc, Cookie: "FANBOXSESSID=original", UserAgent: "session-test"}
}

func TestSessionCookiesRotateWithoutLeaking(t *testing.T) {
	calls := 0
	api := sessionClient(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Hostname() != "api.fanbox.cc" {
			require.Empty(t, req.Header.Get("Cookie"), "login cookies reached a CDN or external host")
			return sessionResponse(req, 200, "download"), nil
		}
		cookies := req.Cookies()
		if calls == 1 {
			require.Len(t, cookies, 1)
			require.Equal(t, "original", cookies[0].Value)
			return sessionResponse(req, 200, `{ "body": [] }`, "FANBOXSESSID=rotated; Path=/; Secure", "__cf_bm=challenge; Path=/; Secure"), nil
		}
		require.Len(t, cookies, 2, "cookies must not be duplicated")
		session, err := req.Cookie("FANBOXSESSID")
		require.NoError(t, err)
		require.Equal(t, "rotated", session.Value)
		cf, err := req.Cookie("__cf_bm")
		require.NoError(t, err)
		require.Equal(t, "challenge", cf.Value)
		return sessionResponse(req, 200, `{ "body": [] }`), nil
	})
	for _, url := range []string{"https://api.fanbox.cc/first", "https://api.fanbox.cc/second", "https://downloads.fanbox.cc/file", "https://drive.google.com/file", "https://api.fanbox.cc.evil.example/file"} {
		resp, err := api.Request(context.Background(), http.MethodGet, url)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}
	require.Equal(t, 5, calls)
}

func TestSessionCookiesRotateDuringRetry(t *testing.T) {
	calls := 0
	api := sessionClient(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return sessionResponse(req, 500, "temporary error", "FANBOXSESSID=rotated; Path=/; Secure", "__cf_bm=challenge; Path=/; Secure"), nil
		}
		require.Len(t, req.Cookies(), 2, "retry must not replay stale or duplicate cookies")
		session, err := req.Cookie("FANBOXSESSID")
		require.NoError(t, err)
		require.Equal(t, "rotated", session.Value)
		return sessionResponse(req, 200, `{ "body": [] }`), nil
	})
	var plans PlanListSupportingResponse
	require.NoError(t, api.RequestAndUnwrapJSON(context.Background(), http.MethodGet, "https://api.fanbox.cc/plan.listSupporting", &plans))
	require.Equal(t, 2, calls)
}

func TestSessionStopsAfterRejection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		cookie string
		body   string
		want   error
	}{
		{"unauthorized", 401, "", "", ErrInvalidSession},
		{"forbidden", 403, "", "", ErrStatusForbidden},
		{"cookie-deleted", 200, "FANBOXSESSID=; Max-Age=0; Path=/", `{ "body": [] }`, ErrInvalidSession},
		{"expired-cookie", 200, "FANBOXSESSID=old; Expires=Thu, 01 Jan 1970 00:00:00 GMT; Path=/", `{ "body": [] }`, ErrInvalidSession},
		{"api-auth-error", 200, "", `{ "error": "authentication_required" }`, ErrInvalidSession},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			api := sessionClient(func(req *http.Request) (*http.Response, error) {
				calls++
				resp := sessionResponse(req, tc.status, tc.body)
				if tc.cookie != "" {
					resp.Header.Add("Set-Cookie", tc.cookie)
				}
				return resp, nil
			})
			var plans PlanListSupportingResponse
			err := api.RequestAndUnwrapJSON(context.Background(), http.MethodGet, "https://api.fanbox.cc/first", &plans)
			require.ErrorIs(t, err, tc.want)
			_, err = api.Request(context.Background(), http.MethodGet, "https://api.fanbox.cc/second")
			require.ErrorIs(t, err, tc.want)
			require.Equal(t, 1, calls, "a rejected session must not generate more network traffic")
		})
	}
}

func TestSessionDoesNotFollowAPIRedirects(t *testing.T) {
	for _, target := range []string{"https://www.fanbox.cc/logout", "https://external.example/login"} {
		t.Run(target, func(t *testing.T) {
			calls := 0
			api := sessionClient(func(req *http.Request) (*http.Response, error) {
				calls++
				resp := sessionResponse(req, 302, "")
				resp.Header.Set("Location", target)
				return resp, nil
			})
			_, err := api.Request(context.Background(), http.MethodGet, "https://api.fanbox.cc/post.info")
			require.ErrorIs(t, err, ErrInvalidSession)
			require.Equal(t, 1, calls, "login/logout redirects must never be visited")
		})
	}
}

func TestSessionRateLimitAlsoAppliesToRetries(t *testing.T) {
	calls := 0
	api := sessionClient(func(req *http.Request) (*http.Response, error) {
		calls++
		return sessionResponse(req, 500, "temporary error"), nil
	})
	api.SetRateLimit(0.01)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := api.Request(ctx, http.MethodGet, "https://api.fanbox.cc/post.info")
	require.Error(t, err)
	require.Equal(t, 1, calls, "retry bypassed the rate limiter")
}

func TestValidateSessionAllowsAccountsWithoutPaidPlans(t *testing.T) {
	api := sessionClient(func(req *http.Request) (*http.Response, error) {
		return sessionResponse(req, 200, `{ "body": { "plans": [] } }`), nil
	})
	require.NoError(t, api.ValidateSession(context.Background()))
	api.Cookie = "" // Configuration is seeded only once; jar updates remain authoritative.
	require.NoError(t, api.ValidateSession(context.Background()))
}

func TestRobustDownloaderDoesNotRetryRejectedSession(t *testing.T) {
	calls := 0
	api := sessionClient(func(req *http.Request) (*http.Response, error) {
		calls++
		return sessionResponse(req, 403, "forbidden"), nil
	})
	err := NewRobustDownloader(api).DownloadWithResume(context.Background(), "https://downloads.fanbox.cc/file", t.TempDir()+"/file", DownloadStateMeta{})
	require.True(t, errors.Is(err, ErrStatusForbidden))
	require.Equal(t, 1, calls, "HEAD failure must not trigger another GET")
}
