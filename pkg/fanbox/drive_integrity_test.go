package fanbox

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

func TestDriveAPIResumeValidatesWholeFileSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "asset.bin")
	require.NoError(t, os.WriteFile(tempFilePath(path), []byte("abc"), 0644))
	client := &http.Client{Transport: sessionTestTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("alt") != "media" {
			return sessionResponse(req, 200, `{"id":"asset","name":"asset.bin","size":"6","mimeType":"application/octet-stream"}`), nil
		}
		require.Equal(t, "bytes=3-", req.Header.Get("Range"))
		resp := sessionResponse(req, 206, "def")
		resp.ContentLength = 3
		resp.Header.Set("Content-Range", "bytes 3-5/6")
		return resp, nil
	})}
	service, err := drive.NewService(context.Background(), option.WithHTTPClient(client), option.WithEndpoint("https://drive-api.example/"), option.WithoutAuthentication())
	require.NoError(t, err)
	downloader := &DriveAPIDownloader{service: service}
	require.NoError(t, downloader.DownloadFile(context.Background(), "asset", dir, DownloadStateMeta{}))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "abcdef", string(data))
}

func TestDriveAPIEmptyResponsePreservesExistingFinal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "asset.bin")
	require.NoError(t, os.WriteFile(path, []byte("existing valid file"), 0644))
	require.NoError(t, os.WriteFile(stateFilePath(path), []byte(`{}`), 0644))
	client := &http.Client{Transport: sessionTestTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("alt") != "media" {
			return sessionResponse(req, 200, `{"id":"asset","name":"asset.bin","size":"6","mimeType":"application/octet-stream"}`), nil
		}
		return sessionResponse(req, 200, ""), nil
	})}
	service, err := drive.NewService(context.Background(), option.WithHTTPClient(client), option.WithEndpoint("https://drive-api.example/"), option.WithoutAuthentication())
	require.NoError(t, err)
	downloader := &DriveAPIDownloader{service: service}
	require.Error(t, downloader.DownloadFile(context.Background(), "asset", dir, DownloadStateMeta{}))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "existing valid file", string(data))
}

type countedDriveBody struct {
	reader io.Reader
	read   int
	closed bool
}

func (b *countedDriveBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n
	return n, err
}

func (b *countedDriveBody) Close() error { b.closed = true; return nil }

func TestDriveSkipsValidFileWithoutDownloadingWholeBody(t *testing.T) {
	t.Setenv("GOOGLE_DRIVE_API_KEY", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "asset.bin")
	require.NoError(t, os.WriteFile(path, []byte("existing valid file"), 0644))
	payload := strings.Repeat("x", 1<<20)
	body := &countedDriveBody{reader: strings.NewReader(payload)}
	client := &http.Client{Transport: sessionTestTransport(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/files/") {
			return sessionResponse(req, 403, "not available"), nil
		}
		resp := sessionResponse(req, 200, "")
		resp.Body = body
		resp.ContentLength = int64(len(payload))
		return resp, nil
	})}
	require.NoError(t, NewDriveDownloader(client, "test").DownloadFile(context.Background(), "https://drive.google.com/file/d/asset/view", dir, DownloadStateMeta{}))
	require.True(t, body.closed)
	require.Less(t, body.read, len(payload))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "existing valid file", string(data))
}

func TestDriveRetriesFromBytesActuallyReceived(t *testing.T) {
	t.Setenv("GOOGLE_DRIVE_API_KEY", "")
	previousDelay := driveDownloadRetryDelay
	driveDownloadRetryDelay = 0
	t.Cleanup(func() { driveDownloadRetryDelay = previousDelay })
	calls := 0
	client := &http.Client{Transport: sessionTestTransport(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/files/") {
			return sessionResponse(req, 403, "not available"), nil
		}
		calls++
		resp := sessionResponse(req, 200, "abc")
		resp.Header.Set("Content-Disposition", `attachment; filename="asset.bin"`)
		resp.ContentLength = 6
		if calls > 1 {
			require.Equal(t, "bytes=3-", req.Header.Get("Range"))
			resp = sessionResponse(req, 206, "def")
			resp.ContentLength = 3
			resp.Header.Set("Content-Disposition", `attachment; filename="asset.bin"`)
			resp.Header.Set("Content-Range", "bytes 3-5/6")
		}
		return resp, nil
	})}
	dir := t.TempDir()
	require.NoError(t, NewDriveDownloader(client, "test").DownloadFile(context.Background(), "https://drive.google.com/file/d/asset/view", dir, DownloadStateMeta{}))
	data, err := os.ReadFile(filepath.Join(dir, "asset.bin"))
	require.NoError(t, err)
	require.Equal(t, "abcdef", string(data))
	require.Equal(t, 2, calls)
}
