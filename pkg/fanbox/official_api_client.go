package fanbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"sync"

	"github.com/hashicorp/go-retryablehttp"
	"golang.org/x/time/rate"
)

type OfficialAPIClient struct {
	HTTPClient     *retryablehttp.Client
	Cookie         string
	UserAgent      string
	rateLimiter    *rate.Limiter
	initOnce       sync.Once
	transport      *sessionTransport
	requestMu      sync.Mutex
	rateLimiterMu  sync.RWMutex
	onJSONReceived JSONReceivedCallback
}

// JSONReceivedCallback is called when JSON is received from the API
type JSONReceivedCallback func(url string, jsonData []byte)

// SetRateLimit sets the rate limit for API requests
func (c *OfficialAPIClient) SetRateLimit(requestsPerSecond float64) {
	c.rateLimiterMu.Lock()
	defer c.rateLimiterMu.Unlock()
	if requestsPerSecond <= 0 {
		c.rateLimiter = nil
		return
	}
	c.rateLimiter = rate.NewLimiter(rate.Limit(requestsPerSecond), 1)
}

// SetJSONCallback sets the callback function to be called when JSON is received
func (c *OfficialAPIClient) SetJSONSetCallback(callback JSONReceivedCallback) {
	c.rateLimiterMu.Lock()
	defer c.rateLimiterMu.Unlock()
	c.onJSONReceived = callback
}

func (c *OfficialAPIClient) Request(ctx context.Context, method string, url string) (*http.Response, error) {
	return c.RequestWithHeaders(ctx, method, url, nil)
}

func (c *OfficialAPIClient) RequestWithHeaders(ctx context.Context, method string, url string, headers map[string]string) (*http.Response, error) {
	c.initOnce.Do(c.initSession)
	req, err := retryablehttp.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("http request building error: %w", err)
	}
	if isFanboxURL(req.URL) {
		// Let the outer HTTP client finish updating its jar before another request.
		c.requestMu.Lock()
		defer c.requestMu.Unlock()
	}
	req.Header.Set("Origin", "https://www.fanbox.cc")
	req.Header.Set("Referer", "https://www.fanbox.cc/")
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if isFanboxURL(req.URL) {
		if err := c.transport.failure(); err != nil {
			return nil, err
		}
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	if isFanboxURL(req.URL) {
		if err := c.transport.failure(); err != nil {
			_ = resp.Body.Close()
			return nil, err
		}
	}
	return resp, nil
}

func (c *OfficialAPIClient) RequestAndUnwrapJSON(ctx context.Context, method string, url string, v interface{}) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("v should be a pointer")
	}
	resp, err := c.Request(ctx, method, url)
	if err != nil {
		return fmt.Errorf("http error: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status is %s", resp.Status)
	}
	c.rateLimiterMu.RLock()
	callback := c.onJSONReceived
	c.rateLimiterMu.RUnlock()
	if callback == nil {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			if errors.Is(err, ErrInvalidSession) {
				c.transport.stop(err)
			}
			return fmt.Errorf("json decoding error: %w", err)
		}
		return nil
	}
	jsonData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	callback(url, jsonData)
	if err := json.Unmarshal(jsonData, v); err != nil {
		if errors.Is(err, ErrInvalidSession) {
			c.transport.stop(err)
		}
		return fmt.Errorf("json decoding error: %w", err)
	}
	return nil
}

var ErrFailedToThumbnailing = fmt.Errorf("failed to thumbnailing")

// fanbox returns HTTP 500 error and response body is "failed to thumbnailing"
// when the image is not available (e.g. too large).
func IsFailedToThumbnailingErr(resp *http.Response) (bool, error) {
	if resp.StatusCode != 500 {
		return false, nil
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return false, fmt.Errorf("parse content type: %w", err)
	}
	if mediaType != "text/plain" {
		return false, nil
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("read response body: %w", err)
	}
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(b))

	if string(b) == "failed to thumbnailing" {
		return true, nil
	}
	return false, nil
}
