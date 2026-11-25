package tlsclient_test

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/hareku/fanbox-dl/internal/tlsclient"
	"github.com/stretchr/testify/require"
)

func TestTransportPreservesOuterCookiesRedirectsAndGzip(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.SetCookie(w, &http.Cookie{Name: "challenge", Value: "rotated", Path: "/"})
			http.Redirect(w, r, "/body", http.StatusFound)
			return
		}
		cookie, err := r.Cookie("challenge")
		if err != nil || cookie.Value != "rotated" {
			http.Error(w, "missing outer cookie", http.StatusUnauthorized)
			return
		}
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			http.Error(w, "missing automatic compression negotiation", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		compressed := gzip.NewWriter(w)
		_, _ = compressed.Write([]byte("decoded response"))
		_ = compressed.Close()
	}))
	defer server.Close()
	transport, err := tlsclient.NewTransportWithOptions(tls_client.NewNoopLogger(), tls_client.WithClientProfile(profiles.Chrome_146_PSK), tls_client.WithInsecureSkipVerify())
	require.NoError(t, err)
	defer transport.CloseIdleConnections()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Transport: transport, Jar: jar, Timeout: 5 * time.Second}
	resp, err := client.Get(server.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "decoded response", string(body))
}
