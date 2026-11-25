package fanbox

import (
	"context"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCheckerWaitsForMissingAssetRepair(t *testing.T) {
	post := crawlPost("30", "2026-06-20T00:00:00Z")
	image := Image{ID: "image", OriginalURL: "https://downloads.fanbox.cc/image.jpg", Extension: "jpg"}
	images := []Image{image}
	post.Body.Images = &images
	client, fixtures := crawlClient(t, [][]Post{{post}})
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	api := sessionClient(func(req *http.Request) (*http.Response, error) {
		if req.URL.Hostname() == "api.fanbox.cc" {
			return sessionResponse(req, 200, fixtures.responses[req.URL.String()]), nil
		}
		if req.Method == http.MethodHead {
			resp := sessionResponse(req, 200, "")
			resp.ContentLength = 1
			resp.Header.Set("Accept-Ranges", "bytes")
			return resp, nil
		}
		close(started)
		<-release
		resp := sessionResponse(req, 206, "x")
		resp.ContentLength = 1
		resp.Header.Set("Content-Range", "bytes 0-0/1")
		return resp, nil
	})
	checker := &Checker{OfficialAPIClient: api, Storage: client.Storage, DownloadOnMissing: true, DownloadWorkers: 2}
	done := make(chan error, 1)
	go func() { done <- checker.Run(context.Background(), "creator") }()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("checker returned before repair started: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("repair did not start")
	}
	select {
	case err := <-done:
		t.Fatalf("checker returned while repair was blocked: %v", err)
	default:
	}
	unblock()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("checker did not finish after repair")
	}
	body, err := os.ReadFile(client.Storage.makeFileName(post, 0, image))
	require.NoError(t, err)
	require.Equal(t, "x", string(body))
}
