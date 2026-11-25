package fanbox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRobustDownloaderStreamsChunkedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Empty(t, r.Header.Get("Range"))
		w.(http.Flusher).Flush() // Force chunked transfer with no Content-Length.
		_, _ = w.Write([]byte("chunked payload"))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "asset")
	api := sessionClient(nil)
	api.HTTPClient.HTTPClient.Transport = http.DefaultTransport
	require.NoError(t, NewRobustDownloader(api).DownloadWithResume(context.Background(), server.URL, path, DownloadStateMeta{}))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "chunked payload", string(data))
}

func TestRobustDownloaderStreamsAndResumes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		partial      string
		status       int
		body         string
		length       int64
		contentRange string
		want         string
	}{
		{name: "known length", status: 200, body: "abcdef", length: 6, want: "abcdef"},
		{name: "missing length", status: 200, body: "abcdef", length: -1, want: "abcdef"},
		{name: "zero unspecified length", status: 200, body: "abcdef", length: 0, want: "abcdef"},
		{name: "resume", partial: "abc", status: 206, body: "def", length: 3, contentRange: "bytes 3-5/6", want: "abcdef"},
		{name: "resume unknown length", partial: "abc", status: 206, body: "def", length: -1, contentRange: "bytes 3-5/6", want: "abcdef"},
		{name: "ignored range restarts", partial: "old bytes", status: 200, body: "abcdef", length: 6, want: "abcdef"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "asset")
			if tc.partial != "" {
				require.NoError(t, os.WriteFile(tempFilePath(path), []byte(tc.partial), 0664))
			}
			calls := 0
			api := sessionClient(func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, http.MethodGet, req.Method)
				if tc.partial == "" {
					require.Empty(t, req.Header.Get("Range"))
				} else {
					require.NotEmpty(t, req.Header.Get("Range"))
				}
				resp := sessionResponse(req, tc.status, tc.body)
				resp.ContentLength = tc.length
				resp.Header.Set("Content-Range", tc.contentRange)
				return resp, nil
			})
			require.NoError(t, NewRobustDownloader(api).DownloadWithResume(context.Background(), "https://downloads.example/asset", path, DownloadStateMeta{AssetID: "asset"}))
			require.Equal(t, 1, calls)
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, tc.want, string(data))
			_, err = os.Stat(tempFilePath(path))
			require.True(t, errors.Is(err, os.ErrNotExist))
			_, err = os.Stat(stateFilePath(path))
			require.True(t, errors.Is(err, os.ErrNotExist))
		})
	}
}

func TestRobustDownloaderRejectsIncompleteBodies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		length int64
	}{
		{name: "empty unknown", length: -1},
		{name: "empty zero", length: 0},
		{name: "short transfer", body: "abc", length: 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "asset")
			require.NoError(t, os.WriteFile(path, []byte("existing good final"), 0664))
			api := sessionClient(func(req *http.Request) (*http.Response, error) {
				resp := sessionResponse(req, 200, tc.body)
				resp.ContentLength = tc.length
				return resp, nil
			})
			require.Error(t, NewRobustDownloader(api).DownloadWithResume(context.Background(), "https://downloads.example/asset", path, DownloadStateMeta{}))
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, "existing good final", string(data))
			if tc.body != "" {
				data, err = os.ReadFile(tempFilePath(path))
				require.NoError(t, err)
				require.Equal(t, tc.body, string(data))
			}
			state, err := loadStateFile(stateFilePath(path))
			require.NoError(t, err)
			require.Equal(t, downloadStateStatusFailed, state.Status)
		})
	}
}

type rejectedDownloadBody struct {
	t      *testing.T
	closed bool
}

func (b *rejectedDownloadBody) Read([]byte) (int, error) {
	b.t.Error("rejected response body must not be read or drained")
	return 0, errors.New("unexpected read")
}

func (b *rejectedDownloadBody) Close() error {
	b.closed = true
	return nil
}

func TestRobustDownloaderRejectsInvalidRangesWithoutChangingFiles(t *testing.T) {
	for _, tc := range []struct {
		name         string
		contentRange string
		length       int64
	}{
		{name: "missing", length: 3},
		{name: "malformed", contentRange: "not a range", length: 3},
		{name: "wrong start", contentRange: "bytes 2-5/6", length: 4},
		{name: "intermediate suffix", contentRange: "bytes 3-4/6", length: 2},
		{name: "length mismatch", contentRange: "bytes 3-5/6", length: 2},
		{name: "unknown total", contentRange: "bytes 3-5/*", length: 3},
		{name: "end exceeds total", contentRange: "bytes 3-6/6", length: 4},
		{name: "trailing junk", contentRange: "bytes 3-5/6 junk", length: 3},
		{name: "overflow", contentRange: "bytes 3-5/9223372036854775808", length: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "asset")
			require.NoError(t, os.WriteFile(path, []byte("good final"), 0664))
			require.NoError(t, os.WriteFile(tempFilePath(path), []byte("abc"), 0664))
			require.NoError(t, os.WriteFile(stateFilePath(path), []byte("existing state"), 0664))
			body := &rejectedDownloadBody{t: t}
			calls := 0
			api := sessionClient(func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "bytes=3-", req.Header.Get("Range"))
				resp := sessionResponse(req, 206, "")
				resp.Body = body
				resp.ContentLength = tc.length
				resp.Header.Set("Content-Range", tc.contentRange)
				return resp, nil
			})
			require.Error(t, NewRobustDownloader(api).DownloadWithResume(context.Background(), "https://downloads.example/asset", path, DownloadStateMeta{}))
			require.Equal(t, 1, calls)
			require.True(t, body.closed)
			for name, want := range map[string]string{path: "good final", tempFilePath(path): "abc", stateFilePath(path): "existing state"} {
				data, err := os.ReadFile(name)
				require.NoError(t, err)
				require.Equal(t, want, string(data))
			}
		})
	}
}

func TestRobustDownloaderStopsOnSessionErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			body := &rejectedDownloadBody{t: t}
			api := sessionClient(func(req *http.Request) (*http.Response, error) {
				calls++
				resp := sessionResponse(req, status, "")
				resp.Body = body
				return resp, nil
			})
			err := NewRobustDownloader(api).DownloadWithResume(context.Background(), "https://downloads.example/asset", filepath.Join(t.TempDir(), "asset"), DownloadStateMeta{})
			if status == http.StatusUnauthorized {
				require.ErrorIs(t, err, ErrInvalidSession)
			} else {
				require.ErrorIs(t, err, ErrStatusForbidden)
			}
			require.Equal(t, 1, calls)
			require.True(t, body.closed)
		})
	}
}
