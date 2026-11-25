package fanbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/hareku/fanbox-dl/internal/ctxval"
	"golang.org/x/net/http2"
)

// Checker validates downloaded content
type Checker struct {
	OfficialAPIClient *OfficialAPIClient
	Storage           *LocalStorage
	SkipFiles         bool
	SkipImages        bool
	SkipTexts         bool
	EnableSaveJSON    bool
	EnableSaveHTML    bool

	// Download options
	DownloadOnMissing  bool
	DownloadWorkers    int
	HTMLGenerator      *HTMLGenerator
	GigafileDownloader *GigafileDownloader
	DriveDownloader    *DriveDownloader
	CreatorName        string
}

// CheckResult represents the result of checking a post
type CheckResult struct {
	PostID        string
	PostTitle     string
	MissingFiles  []string
	MissingImages []string
	MissingText   bool
	MissingJSON   bool
	MissingHTML   bool
	IsComplete    bool
}

// CheckResults aggregates all check results
type CheckResults struct {
	TotalPosts      int
	CompletePosts   int
	IncompletePosts int
	Results         []CheckResult
}

func (c *Checker) Run(ctx context.Context, creatorID string) error {
	ctx = ctxval.AddSlogAttrs(ctx, slog.String("creator_id", creatorID))

	// Get pagination
	var pagination Pagination
	if err := c.OfficialAPIClient.RequestAndUnwrapJSON(
		ctx, http.MethodGet,
		fmt.Sprintf("https://api.fanbox.cc/post.paginateCreator?%s", func() string {
			q := url.Values{}
			q.Set("creatorId", creatorID)
			return q.Encode()
		}()),
		&pagination,
	); err != nil {
		return fmt.Errorf("get pagination: %w", err)
	}
	slog.DebugContext(ctx, "Found pages", "pages", len(pagination.Pages))

	results := &CheckResults{}

	for i, page := range pagination.Pages {
		content := ListCreatorResponse{}
		err := c.OfficialAPIClient.RequestAndUnwrapJSON(ctx, http.MethodGet, page, &content)
		if err != nil {
			return fmt.Errorf("list posts of %q: %w", creatorID, err)
		}
		slog.DebugContext(ctx, "Found posts",
			"page", i+1,
			"posts", len(content.Body),
		)

		for _, post := range content.Body {
			result, err := c.checkPost(ctx, post)
			if err != nil {
				if errors.Is(err, ErrInvalidSession) || errors.Is(err, ErrStatusForbidden) || ctx.Err() != nil {
					return err
				}
				slog.WarnContext(ctx, "Failed to check post", "post_id", post.ID, "error", err)
				continue
			}
			results.TotalPosts++
			results.Results = append(results.Results, result)
			if result.IsComplete {
				results.CompletePosts++
			} else {
				results.IncompletePosts++
				c.logIncompletePost(ctx, result)
			}
		}
	}

	c.logSummary(ctx, results)
	return nil
}

func (c *Checker) checkPost(ctx context.Context, post Post) (CheckResult, error) {
	result := CheckResult{
		PostID:     post.ID,
		PostTitle:  post.Title,
		IsComplete: true,
	}

	if post.IsRestricted {
		return result, nil
	}
	// Get post info for full details
	postResp := PostInfoResponse{}
	err := c.OfficialAPIClient.RequestAndUnwrapJSON(
		ctx, http.MethodGet,
		fmt.Sprintf("https://api.fanbox.cc/post.info?%s", func() string {
			q := url.Values{}
			q.Set("postId", post.ID)
			return q.Encode()
		}()),
		&postResp,
	)
	if err != nil {
		return result, fmt.Errorf("get post: %w", err)
	}
	post = postResp.Body
	if post.IsRestricted || post.Body == nil {
		return result, nil
	}

	// Check text content
	if !c.SkipTexts && post.GetTextContent() != "" {
		textExists, err := c.Storage.TextExists(post)
		if err != nil {
			return result, err
		}
		if !textExists {
			result.MissingText = true
			result.IsComplete = false
			// Download missing text if enabled
			if c.DownloadOnMissing {
				c.downloadText(ctx, post)
			}
		}
	}

	// Check JSON
	if c.EnableSaveJSON {
		jsonExists, err := c.Storage.JSONExists(post)
		if err != nil {
			return result, err
		}
		if !jsonExists {
			result.MissingJSON = true
			result.IsComplete = false
			// Download missing JSON if enabled
			if c.DownloadOnMissing {
				c.downloadJSON(ctx, post)
			}
		}
	}

	// Check HTML
	if c.EnableSaveHTML {
		htmlExists, err := c.Storage.HTMLExists(post)
		if err != nil {
			return result, err
		}
		if !htmlExists {
			result.MissingHTML = true
			result.IsComplete = false
			// Download missing HTML if enabled
			if c.DownloadOnMissing {
				if err := c.downloadHTML(ctx, post); err != nil {
					return result, err
				}
			}
		}
	}

	// Check downloadable files (images and files)
	downloadables := post.ListDownloadable()
	missingDownloadables := []struct {
		index int
		d     Downloadable
	}{}

	for i, d := range downloadables {
		if _, ok := d.(File); ok && c.SkipFiles {
			continue
		}
		if _, ok := d.(Image); ok && c.SkipImages {
			continue
		}

		if d.GetID() == "" {
			continue
		}

		exists, err := c.Storage.Exist(post, i, d)
		if err != nil {
			return result, err
		}

		if !exists {
			fileName := c.Storage.MakeFileName(post, i, d)
			if _, ok := d.(Image); ok {
				result.MissingImages = append(result.MissingImages, filepath.Base(fileName))
			} else {
				result.MissingFiles = append(result.MissingFiles, filepath.Base(fileName))
			}
			result.IsComplete = false

			// Collect missing assets for the bounded repair pool.
			if c.DownloadOnMissing {
				missingDownloadables = append(missingDownloadables, struct {
					index int
					d     Downloadable
				}{i, d})
			}
		}
	}

	// Keep repair work bounded and finish it before moving to the next post.
	if c.DownloadOnMissing && len(missingDownloadables) > 0 {
		if err := c.downloadMissingAssets(ctx, post, missingDownloadables); err != nil {
			return result, err
		}
	}

	return result, nil
}

func (c *Checker) logIncompletePost(ctx context.Context, result CheckResult) {
	slog.InfoContext(ctx, "Incomplete post",
		"post_id", result.PostID,
		"title", result.PostTitle,
	)

	if result.MissingText {
		slog.InfoContext(ctx, "  Missing: text content")
	}
	if result.MissingJSON {
		slog.InfoContext(ctx, "  Missing: JSON")
	}
	if result.MissingHTML {
		slog.InfoContext(ctx, "  Missing: HTML")
	}
	for _, img := range result.MissingImages {
		slog.InfoContext(ctx, "  Missing image", "file", img)
	}
	for _, file := range result.MissingFiles {
		slog.InfoContext(ctx, "  Missing file", "file", file)
	}
}

func (c *Checker) logSummary(ctx context.Context, results *CheckResults) {
	if results.IncompletePosts > 0 {
		slog.WarnContext(ctx, "Check summary",
			"total", results.TotalPosts,
			"complete", results.CompletePosts,
			"incomplete", results.IncompletePosts,
		)
	} else {
		slog.InfoContext(ctx, "Check summary - All posts are complete!",
			"total", results.TotalPosts,
		)
	}
}

// downloadText saves missing text content.
func (c *Checker) downloadText(ctx context.Context, post Post) {
	ctx = ctxval.AddSlogAttrs(ctx, slog.String("post_id", post.ID), slog.String("title", post.Title))
	slog.InfoContext(ctx, "Downloading missing text content")

	if err := c.Storage.SaveText(post); err != nil {
		slog.ErrorContext(ctx, "Failed to download text content", "error", err)
	} else {
		slog.InfoContext(ctx, "Successfully downloaded text content")
	}
}

// downloadJSON saves missing JSON.
func (c *Checker) downloadJSON(ctx context.Context, post Post) {
	ctx = ctxval.AddSlogAttrs(ctx, slog.String("post_id", post.ID), slog.String("title", post.Title))
	slog.InfoContext(ctx, "Downloading missing JSON")

	jsonData, err := json.Marshal(post)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to marshal JSON", "error", err)
		return
	}

	if err := c.Storage.SaveJSON(post, jsonData); err != nil {
		slog.ErrorContext(ctx, "Failed to download JSON", "error", err)
	} else {
		slog.InfoContext(ctx, "Successfully downloaded JSON")
	}
}

// downloadHTML downloads missing HTML and its external files.
func (c *Checker) downloadHTML(ctx context.Context, post Post) error {
	ctx = ctxval.AddSlogAttrs(ctx, slog.String("post_id", post.ID), slog.String("title", post.Title))
	slog.InfoContext(ctx, "Downloading missing HTML")

	if c.HTMLGenerator == nil {
		return fmt.Errorf("HTML generator not configured")
	}

	htmlContent := c.HTMLGenerator.GenerateHTML(post, c.CreatorName)
	if err := c.Storage.SaveHTML(post, htmlContent); err != nil {
		return fmt.Errorf("save HTML: %w", err)
	}
	return c.handleExternalLinks(ctx, post, htmlContent)
}

// handleExternalLinks extracts and downloads external links from HTML content
func (c *Checker) handleExternalLinks(ctx context.Context, post Post, htmlContent string) error {
	saveDir := c.Storage.makePostDir(post)
	var tasks []func() error

	// Handle gigafile links
	if c.GigafileDownloader != nil {
		gigafileURLs := ExtractGigafileURLs(htmlContent)
		if len(gigafileURLs) > 0 {
			slog.InfoContext(ctx, "Found gigafile links", "count", len(gigafileURLs))
			for _, url := range gigafileURLs {
				tasks = append(tasks, func() error {
					slog.InfoContext(ctx, "Downloading gigafile", "url", url)
					meta := DownloadStateMeta{
						CreatorID: post.CreatorID,
						PostID:    post.ID,
						PostTitle: post.Title,
						AssetType: "gigafile",
						URL:       url,
					}
					return c.GigafileDownloader.DownloadFile(ctx, url, saveDir, meta)
				})
			}
		}
	}

	// Handle Google Drive links
	if c.DriveDownloader != nil {
		driveURLs := ExtractDriveURLs(htmlContent)
		if len(driveURLs) > 0 {
			slog.InfoContext(ctx, "Found Google Drive links", "count", len(driveURLs))
			for _, url := range driveURLs {
				tasks = append(tasks, func() error {
					slog.InfoContext(ctx, "Downloading Google Drive file", "url", url)
					meta := DownloadStateMeta{
						CreatorID: post.CreatorID,
						PostID:    post.ID,
						PostTitle: post.Title,
						AssetType: "drive",
						URL:       url,
					}
					return c.DriveDownloader.DownloadFile(ctx, url, saveDir, meta)
				})
			}
		}
	}
	return runDownloadTasks(ctx, tasks, c.DownloadWorkers)
}

// downloadMissingAssets downloads missing assets with a bounded worker pool.
func (c *Checker) downloadMissingAssets(ctx context.Context, post Post, missingDownloadables []struct {
	index int
	d     Downloadable
}) error {
	ctx = ctxval.AddSlogAttrs(ctx, slog.String("post_id", post.ID), slog.String("title", post.Title))
	slog.InfoContext(ctx, "Downloading missing assets", "count", len(missingDownloadables))

	workers := c.DownloadWorkers
	if workers <= 0 {
		workers = 1
	}

	tasks := make([]func() error, 0, len(missingDownloadables))
	for _, item := range missingDownloadables {
		item := item
		tasks = append(tasks, func() error {
			return c.downloadAsset(ctx, post, item.index, item.d)
		})
	}

	return runDownloadTasks(ctx, tasks, workers)
}

// downloadAsset downloads a single asset
func (c *Checker) downloadAsset(ctx context.Context, post Post, order int, d Downloadable) error {
	assetType := "unknown"
	switch d.(type) {
	case Image:
		assetType = "image"
	case File:
		assetType = "file"
	}

	ctx = ctxval.AddSlogAttrs(ctx, slog.String("asset_type", assetType), slog.String("asset_id", d.GetID()))

	if err := c.downloadWithRetry(ctx, post, order, d); err != nil {
		slog.ErrorContext(ctx, "Failed to download asset", "error", err)
		return err
	}

	slog.InfoContext(ctx, "Successfully downloaded asset")
	return nil
}

// downloadWithRetry downloads an asset with improved retry logic and resume support
func (c *Checker) downloadWithRetry(ctx context.Context, post Post, order int, d Downloadable) error {
	shouldRetry := func(err error) bool {
		// Retry on network errors
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return true
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return true
		}
		if errors.Is(err, context.Canceled) {
			return true
		}

		// Retry on network operation errors
		var opErr *net.OpError
		if errors.As(err, &opErr) {
			return true
		}

		// Retry on HTTP/2 errors
		var goAwayErr *http2.GoAwayError
		if errors.As(err, &goAwayErr) {
			return true
		}

		// Check error string for timeout errors
		errStr := err.Error()
		return containsTimeoutError(errStr)
	}

	var lastErr error
	waitDur := 2 * time.Second
	maxRetries := 10

	for retry := 0; retry < maxRetries; retry++ {
		if retry > 0 {
			slog.InfoContext(ctx, "Retrying download",
				"attempt", retry+1,
				"max_attempts", maxRetries,
				"wait", waitDur,
				"last_error", lastErr)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(waitDur):
			}
			// Exponential backoff
			waitDur = waitDur * 2
			if waitDur > 30*time.Second {
				waitDur = 30 * time.Second
			}
		}

		if err := c.download(ctx, post, order, d); err != nil {
			lastErr = err
			if errors.Is(err, ErrInvalidSession) || errors.Is(err, ErrStatusForbidden) || ctx.Err() != nil {
				return err
			}
			if shouldRetry(err) {
				slog.WarnContext(ctx, "Download failed, will retry",
					"error", err,
					"attempt", retry+1)
				continue
			}
			return err
		}
		return nil
	}

	return fmt.Errorf("download failed after %d retries, last error: %w", maxRetries, lastErr)
}

// containsTimeoutError checks if error string contains timeout-related keywords
func containsTimeoutError(errStr string) bool {
	timeoutKeywords := []string{
		"timeout",
		"deadline exceeded",
		"context canceled",
		"canceled",
		"broken pipe",
		"connection reset",
		"connection refused",
		"unexpected EOF",
	}

	lowerErr := strings.ToLower(errStr)
	for _, keyword := range timeoutKeywords {
		if strings.Contains(lowerErr, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

// download performs actual download with resume support using RobustDownloader
func (c *Checker) download(ctx context.Context, post Post, order int, d Downloadable) error {
	filePath := c.Storage.makeFileName(post, order, d)
	targetURL := d.GetURL()

	// Determine asset type for state
	assetType := "unknown"
	switch d.(type) {
	case Image:
		assetType = "image"
	case File:
		assetType = "file"
	}

	meta := DownloadStateMeta{
		CreatorID: post.CreatorID,
		PostID:    post.ID,
		PostTitle: post.Title,
		AssetID:   d.GetID(),
		AssetType: assetType,
		URL:       targetURL,
	}

	// Use robust downloader with chunked resume
	downloader := NewRobustDownloader(c.OfficialAPIClient)
	return downloader.DownloadWithResume(ctx, targetURL, filePath, meta)
}
