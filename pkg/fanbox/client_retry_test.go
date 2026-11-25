package fanbox

import (
	"context"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/bogdanfinn/fhttp/http2"
	"github.com/stretchr/testify/require"
)

type goAwayResponseBody struct {
	read bool
}

func (b *goAwayResponseBody) Read(p []byte) (int, error) {
	if b.read {
		return 0, io.EOF
	}
	b.read = true
	return copy(p, "abc"), http2.GoAwayError{}
}

func (*goAwayResponseBody) Close() error { return nil }

func TestClientResumesAfterTLSClientGoAway(t *testing.T) {
	calls := 0
	api := sessionClient(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			resp := sessionResponse(req, 200, "")
			resp.Body = &goAwayResponseBody{}
			resp.ContentLength = 6
			return resp, nil
		}
		require.Equal(t, "bytes=3-", req.Header.Get("Range"))
		resp := sessionResponse(req, 206, "def")
		resp.ContentLength = 3
		return resp, nil
	})
	post := crawlPost("30", "2026-06-20T00:00:00Z")
	image := Image{ID: "image", OriginalURL: "https://downloads.fanbox.cc/image.jpg", Extension: "jpg"}
	client := &Client{OfficialAPIClient: api, Storage: &LocalStorage{SaveDir: t.TempDir()}}
	require.NoError(t, client.downloadWithRetry(context.Background(), post, 0, image))
	data, err := os.ReadFile(client.Storage.makeFileName(post, 0, image))
	require.NoError(t, err)
	require.Equal(t, "abcdef", string(data))
	require.Equal(t, 2, calls)
}
