package fanbox

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
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
		require.Equal(t, http.MethodGet, req.Method)
		close(started)
		<-release
		resp := sessionResponse(req, 200, "x")
		resp.ContentLength = 1
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

func checkerWriteFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
}

func checkerMixedPost() (Post, []Downloadable, []int) {
	post := crawlPost("30", "2026-06-20T00:00:00Z")
	imageID, fileID, secondImageID := "image", "file", "second-image"
	image := Image{ID: imageID, OriginalURL: "https://downloads.fanbox.cc/image.jpg", Extension: "jpg"}
	file := File{ID: fileID, Name: "attachment", URL: "https://downloads.fanbox.cc/file.zip", Extension: "zip"}
	secondImage := Image{ID: secondImageID, OriginalURL: "https://downloads.fanbox.cc/second.jpg", Extension: "jpg"}
	post.Body.Blocks = &[]Block{{Type: "image", ImageID: &imageID}, {Type: "file", FileID: &fileID}, {Type: "image", ImageID: &secondImageID}}
	post.Body.ImageMap = &map[string]Image{imageID: image, secondImageID: secondImage}
	post.Body.FileMap = &map[string]File{fileID: file}
	return post, []Downloadable{image, file, secondImage}, []int{0, 0, 1}
}

func TestCheckerEmptyMixedAssets(t *testing.T) {
	for _, repair := range []bool{false, true} {
		name := "check-only"
		if repair {
			name = "repair"
		}
		t.Run(name, func(t *testing.T) {
			post, assets, orders := checkerMixedPost()
			client, fixtures := crawlClient(t, [][]Post{{post}})
			var mu sync.Mutex
			var requests []string
			api := sessionClient(func(req *http.Request) (*http.Response, error) {
				if req.URL.Hostname() == "api.fanbox.cc" {
					return sessionResponse(req, 200, fixtures.responses[req.URL.String()]), nil
				}
				mu.Lock()
				requests = append(requests, req.URL.String())
				mu.Unlock()
				resp := sessionResponse(req, 200, "repaired")
				resp.ContentLength = int64(len("repaired"))
				return resp, nil
			})
			for i, asset := range assets {
				content := ""
				if i == 2 {
					content = "valid existing image"
				}
				checkerWriteFile(t, client.Storage.makeFileName(post, orders[i], asset), content)
			}
			checker := &Checker{OfficialAPIClient: api, Storage: client.Storage, SkipTexts: true, DownloadOnMissing: repair, DownloadWorkers: 2}
			result, err := checker.checkPost(context.Background(), post)
			require.NoError(t, err)
			require.False(t, result.IsComplete)
			require.Equal(t, []string{filepath.Base(client.Storage.makeFileName(post, 0, assets[0]))}, result.MissingImages)
			require.Equal(t, []string{filepath.Base(client.Storage.makeFileName(post, 0, assets[1]))}, result.MissingFiles)
			for i, asset := range assets {
				data, err := os.ReadFile(client.Storage.makeFileName(post, orders[i], asset))
				require.NoError(t, err)
				expected := ""
				if repair {
					expected = "repaired"
				}
				if i == 2 {
					expected = "valid existing image"
				}
				require.Equal(t, expected, string(data))
			}
			if repair {
				require.ElementsMatch(t, []string{assets[0].GetURL(), assets[1].GetURL()}, requests)
				result, err = checker.checkPost(context.Background(), post)
				require.NoError(t, err)
				require.True(t, result.IsComplete)
				require.Len(t, requests, 2)
			} else {
				require.Empty(t, requests)
			}
		})
	}
}

func TestCheckerRegeneratesEmptyExports(t *testing.T) {
	post := crawlPost("30", "2026-06-20T00:00:00Z")
	client, _ := crawlClient(t, [][]Post{{post}})
	client.Storage.EnableSaveJSON = true
	client.Storage.EnableSaveHTML = true
	paths := []string{client.Storage.makeTextFileName(post), client.Storage.makeJSONFileName(post), client.Storage.makeHTMLFileName(post)}
	for _, path := range paths {
		checkerWriteFile(t, path, "")
	}
	checker := &Checker{OfficialAPIClient: client.OfficialAPIClient, Storage: client.Storage, DownloadOnMissing: true, EnableSaveJSON: true, EnableSaveHTML: true, HTMLGenerator: &HTMLGenerator{Enable: true}}
	result, err := checker.checkPost(context.Background(), post)
	require.NoError(t, err)
	require.False(t, result.IsComplete)
	require.True(t, result.MissingText)
	require.True(t, result.MissingJSON)
	require.True(t, result.MissingHTML)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(data), "content-30")
	}
	result, err = checker.checkPost(context.Background(), post)
	require.NoError(t, err)
	require.True(t, result.IsComplete)
}

func TestCheckerRunReturnsRepairErrorsAndContinuesScanning(t *testing.T) {
	first := crawlPost("30", "2026-06-20T00:00:00Z")
	second := crawlPost("20", "2026-06-19T00:00:00Z")
	image := Image{ID: "image", OriginalURL: "https://downloads.fanbox.cc/image.jpg", Extension: "jpg"}
	first.Body.Images = &[]Image{image}
	client, fixtures := crawlClient(t, [][]Post{{first}, {second}})
	api := sessionClient(func(req *http.Request) (*http.Response, error) {
		if req.URL.Hostname() == "api.fanbox.cc" {
			return sessionResponse(req, 200, fixtures.responses[req.URL.String()]), nil
		}
		resp := sessionResponse(req, 200, "")
		resp.ContentLength = 0
		return resp, nil
	})
	checkerWriteFile(t, client.Storage.makeFileName(first, 0, image), "")
	checkerWriteFile(t, client.Storage.makeTextFileName(first), "valid saved text")
	checker := &Checker{OfficialAPIClient: api, Storage: client.Storage, DownloadOnMissing: true}
	require.Error(t, checker.Run(context.Background(), "creator"))
	data, err := os.ReadFile(client.Storage.makeTextFileName(first))
	require.NoError(t, err)
	require.Equal(t, "valid saved text", string(data))
	data, err = os.ReadFile(client.Storage.makeTextFileName(second))
	require.NoError(t, err)
	require.Equal(t, "content-20", string(data))
	data, err = os.ReadFile(client.Storage.makeFileName(first, 0, image))
	require.NoError(t, err)
	require.Empty(t, data)
}

func TestCheckerPropagatesExportSaveFailures(t *testing.T) {
	for _, kind := range []string{"text", "json"} {
		t.Run(kind, func(t *testing.T) {
			post := crawlPost("30", "2026-06-20T00:00:00Z")
			client, _ := crawlClient(t, [][]Post{{post}})
			client.Storage.EnableSaveJSON = true
			checker := &Checker{OfficialAPIClient: client.OfficialAPIClient, Storage: client.Storage, DownloadOnMissing: true, SkipTexts: kind != "text", EnableSaveJSON: kind == "json"}
			path := client.Storage.makeTextFileName(post)
			if kind == "json" {
				path = client.Storage.makeJSONFileName(post)
			}
			require.NoError(t, os.MkdirAll(path, 0755))
			require.Error(t, checker.Run(context.Background(), "creator"))
		})
	}
}

func TestCheckerRepairsExternalFileWithExistingHTML(t *testing.T) {
	post := crawlPost("30", "2026-06-20T00:00:00Z")
	post.Body.Text = "https://12.gigafile.nu/1234-abcdef1234567890123456789012345678"
	client, _ := crawlClient(t, [][]Post{{post}})
	client.Storage.EnableSaveHTML = true
	checkerWriteFile(t, client.Storage.makeHTMLFileName(post), "valid saved HTML")
	externalPath := filepath.Join(client.Storage.makePostDir(post), "external.bin")
	checkerWriteFile(t, externalPath, "")
	transport := &gigafileTestTransport{cookie: "test-cookie", filename: "external.bin", content: []byte("external content")}
	checker := &Checker{OfficialAPIClient: client.OfficialAPIClient, Storage: client.Storage, SkipTexts: true, EnableSaveHTML: true, DownloadOnMissing: true, HTMLGenerator: &HTMLGenerator{Enable: true}, GigafileDownloader: NewGigafileDownloader(&http.Client{Transport: transport}, "test-agent")}
	_, err := checker.checkPost(context.Background(), post)
	require.NoError(t, err)
	data, err := os.ReadFile(externalPath)
	require.NoError(t, err)
	require.Equal(t, "external content", string(data))
	data, err = os.ReadFile(client.Storage.makeHTMLFileName(post))
	require.NoError(t, err)
	require.Equal(t, "valid saved HTML", string(data))
}
