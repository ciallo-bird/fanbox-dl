package fanbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func crawlPost(id, date string) Post {
	return Post{ID: id, CreatorID: "creator", Title: "post-" + id, PublishedDateTime: date, Body: &PostBody{Text: "content-" + id}}
}

func crawlJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

func crawlClient(t *testing.T, pages [][]Post) (*Client, *staticResponseTransport) {
	t.Helper()
	responses := map[string]string{}
	urls := make([]string, len(pages))
	for i, posts := range pages {
		urls[i] = fmt.Sprintf("https://api.fanbox.cc/post.listCreator?page=%d", i)
		responses[urls[i]] = crawlJSON(t, map[string]any{"body": map[string]any{"posts": posts}})
		for _, post := range posts {
			responses["https://api.fanbox.cc/post.info?postId="+post.ID] = crawlJSON(t, map[string]any{"body": map[string]any{"post": post}})
		}
	}
	responses["https://api.fanbox.cc/post.paginateCreator?creatorId=creator"] = crawlJSON(t, map[string]any{"body": map[string]any{"pageUrls": urls}})
	transport := &staticResponseTransport{responses: responses}
	return &Client{OfficialAPIClient: newStaticOfficialAPIClient(transport), Storage: &LocalStorage{SaveDir: t.TempDir()}, CheckAllPosts: true}, transport
}

func TestCrawlHandlesNewPostsBeforeExistingCheckpoint(t *testing.T) {
	latest := crawlPost("30", "2026-06-30T00:00:00Z")
	older := crawlPost("20", "2026-06-20T00:00:00Z")
	state, err := NewStateManager(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, state.UpdateLastPostID("creator", 20))
	pinned := crawlPost("10", "2025-01-01T00:00:00Z")
	pinned.IsPinned = true
	client, transport := crawlClient(t, [][]Post{{pinned, latest, older}})
	client.StateManager = state
	require.NoError(t, client.Run(context.Background(), "creator"))
	text, err := os.ReadFile(client.Storage.makeTextFileName(latest))
	require.NoError(t, err)
	require.Equal(t, "content-30", string(text))
	id, ok := state.GetLastPostID("creator")
	require.True(t, ok)
	require.EqualValues(t, 30, id)
	require.Equal(t, []string{"https://api.fanbox.cc/post.paginateCreator?creatorId=creator", "https://api.fanbox.cc/post.listCreator?page=0", "https://api.fanbox.cc/post.info?postId=30"}, transport.requestURLs())
}

func TestCrawlEndDateKeepsFetchingOlderPages(t *testing.T) {
	first := crawlPost("30", "2026-06-20T00:00:00Z")
	second := crawlPost("20", "2026-06-10T00:00:00Z")
	client, _ := crawlClient(t, [][]Post{{first}, {second}})
	end := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
	client.EndDate = &end
	require.NoError(t, client.Run(context.Background(), "creator"))
	for _, post := range []Post{first, second} {
		text, err := os.ReadFile(client.Storage.makeTextFileName(post))
		require.NoError(t, err)
		require.Equal(t, "content-"+post.ID, string(text))
	}
}

func TestCrawlStartDateStopsOldPagesButNotPinnedOnlyPages(t *testing.T) {
	pinned := crawlPost("10", "2025-01-01T00:00:00Z")
	pinned.IsPinned = true
	current := crawlPost("30", "2026-06-20T00:00:00Z")
	old := crawlPost("20", "2026-05-01T00:00:00Z")
	older := crawlPost("15", "2026-04-01T00:00:00Z")
	client, transport := crawlClient(t, [][]Post{{pinned}, {current}, {old}, {older}})
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	client.StartDate = &start
	require.NoError(t, client.Run(context.Background(), "creator"))
	text, err := os.ReadFile(client.Storage.makeTextFileName(current))
	require.NoError(t, err)
	require.Equal(t, "content-30", string(text))
	for _, url := range transport.requestURLs() {
		require.NotEqual(t, "https://api.fanbox.cc/post.listCreator?page=3", url)
	}
}

func TestCrawlDoesNotAdvanceCheckpointOnDryRunOrFailure(t *testing.T) {
	for _, mode := range []string{"dry-run", "failed-post"} {
		t.Run(mode, func(t *testing.T) {
			post := crawlPost("30", "2026-06-20T00:00:00Z")
			client, transport := crawlClient(t, [][]Post{{post}})
			state, err := NewStateManager(t.TempDir())
			require.NoError(t, err)
			require.NoError(t, state.UpdateLastPostID("creator", 20))
			client.StateManager = state
			if mode == "dry-run" {
				client.DryRun = true
				require.NoError(t, client.Run(context.Background(), "creator"))
			} else {
				transport.responses["https://api.fanbox.cc/post.info?postId=30"] = "not-json"
				require.Error(t, client.Run(context.Background(), "creator"))
			}
			id, _ := state.GetLastPostID("creator")
			require.EqualValues(t, 20, id)
		})
	}
}

func TestCrawlSkipsEmptyPostInfo(t *testing.T) {
	post := crawlPost("30", "2026-06-20T00:00:00Z")
	client, transport := crawlClient(t, [][]Post{{post}})
	client.Storage.EnableSaveJSON = true
	transport.responses["https://api.fanbox.cc/post.info?postId=30"] = `{ "body": { "post": null } }`
	require.NoError(t, client.Run(context.Background(), "creator"))
	entries, err := os.ReadDir(client.Storage.SaveDir)
	require.NoError(t, err)
	require.Empty(t, entries, "empty post info must not create bogus files")
}

func TestCrawlSkipOnErrorDoesNotSuppressSessionFailure(t *testing.T) {
	post := crawlPost("30", "2026-06-20T00:00:00Z")
	images := []Image{{ID: "image", OriginalURL: "https://downloads.fanbox.cc/image.jpg", Extension: "jpg"}}
	post.Body.Images = &images
	client, transport := crawlClient(t, [][]Post{{post}})
	client.SkipOnError = true
	client.OfficialAPIClient = sessionClient(func(req *http.Request) (*http.Response, error) {
		if strings.HasPrefix(req.URL.String(), "https://downloads.fanbox.cc/") {
			return sessionResponse(req, 403, "forbidden"), nil
		}
		return sessionResponse(req, 200, transport.responses[req.URL.String()]), nil
	})
	require.ErrorIs(t, client.Run(context.Background(), "creator"), ErrStatusForbidden)
}
