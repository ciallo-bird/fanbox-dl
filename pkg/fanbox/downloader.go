package fanbox

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// RobustDownloader streams downloads and resumes existing partial files.
type RobustDownloader struct {
	client *OfficialAPIClient
}

// NewRobustDownloader creates a downloader using the supplied API client.
func NewRobustDownloader(client *OfficialAPIClient) *RobustDownloader {
	return &RobustDownloader{client: client}
}

// DownloadWithResume downloads a file, leaving failed transfers available for resume.
func (rd *RobustDownloader) DownloadWithResume(ctx context.Context, url string, filePath string, meta DownloadStateMeta) error {
	rangeStart, _, err := tempFileOffset(filePath)
	if err != nil {
		return fmt.Errorf("check temp file: %w", err)
	}
	var headers map[string]string
	if rangeStart > 0 {
		headers = map[string]string{"Range": fmt.Sprintf("bytes=%d-", rangeStart)}
	}
	resp, err := rd.client.RequestWithHeaders(ctx, http.MethodGet, url, headers)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	totalBytes := resp.ContentLength
	appendMode := false
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return ErrInvalidSession
	case http.StatusForbidden:
		return ErrStatusForbidden
	case http.StatusOK:
		// A server ignoring Range has returned the full representation.
		rangeStart = 0
	case http.StatusPartialContent:
		totalBytes, err = downloadResponseSize(resp, rangeStart)
		if err != nil {
			return err
		}
		appendMode = rangeStart > 0
	default:
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	return saveReaderWithStateAt(ctx, filePath, resp.Body, meta, totalBytes, 0664, rangeStart, appendMode)
}

// downloadResponseSize returns the expected final size without consuming the body.
func downloadResponseSize(resp *http.Response, startOffset int64) (int64, error) {
	switch resp.StatusCode {
	case http.StatusOK:
		return resp.ContentLength, nil
	case http.StatusPartialContent:
		return completeRangeTotal(resp.Header.Get("Content-Range"), startOffset, resp.ContentLength)
	default:
		return 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
}

// completeRangeTotal accepts only a complete suffix of the requested representation.
// Validate headers before opening the partial file so rejected responses cannot damage it.
func completeRangeTotal(value string, expectedStart, contentLength int64) (int64, error) {
	invalid := func() (int64, error) {
		return 0, fmt.Errorf("invalid Content-Range %q for offset %d and length %d", value, expectedStart, contentLength)
	}
	span, ok := strings.CutPrefix(value, "bytes ")
	if !ok {
		return invalid()
	}
	positions, totalText, ok := strings.Cut(span, "/")
	if !ok {
		return invalid()
	}
	startText, endText, ok := strings.Cut(positions, "-")
	if !ok {
		return invalid()
	}
	parse := func(text string) (int64, error) {
		if strings.HasPrefix(text, "+") {
			return 0, fmt.Errorf("invalid byte position")
		}
		n, err := strconv.ParseUint(text, 10, 63)
		return int64(n), err
	}
	start, startErr := parse(startText)
	end, endErr := parse(endText)
	total, totalErr := parse(totalText)
	if startErr != nil || endErr != nil || totalErr != nil ||
		start != expectedStart || total <= 0 || end < start || end != total-1 {
		return invalid()
	}
	if contentLength >= 0 && contentLength != end-start+1 {
		return invalid()
	}
	return total, nil
}
