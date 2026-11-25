package fanbox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreatorIDLister_Do_ResponseBodies(t *testing.T) {
	const (
		listSupportingURL = "https://api.fanbox.cc/plan.listSupporting"
		listFollowingURL  = "https://api.fanbox.cc/creator.listFollowing"
	)

	tests := []struct {
		name       string
		supporting string
		following  string
		want       []string
	}{
		{
			name:       "legacy responses",
			supporting: listSupportingLegacyResponse,
			following:  listFollowingLegacyResponse,
			want:       []string{"supported-creator", "followed-creator", "shared-creator"},
		},
		{
			name:       "current responses",
			supporting: listSupportingCurrentResponse,
			following:  listFollowingCurrentResponse,
			want:       []string{"supported-creator", "followed-creator", "shared-creator"},
		},
		{
			name:       "null bodies",
			supporting: `{"body":null}`,
			following:  `{"body":null}`,
		},
		{
			name:       "empty wrapped arrays",
			supporting: `{"body":{"plans":[]}}`,
			following:  `{"body":{"creators":[]}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := &staticResponseTransport{responses: map[string]string{
				listSupportingURL: tt.supporting,
				listFollowingURL:  tt.following,
			}}
			lister := CreatorIDLister{OfficialAPIClient: newStaticOfficialAPIClient(transport)}
			creatorIDs, err := lister.Do(context.Background(), &CreatorIDListerDoInput{
				IncludeSupporting: true,
				IncludeFollowing:  true,
			})
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.want, creatorIDs)
			assert.Equal(t, []string{listSupportingURL, listFollowingURL}, transport.requestURLs())
		})
	}
}

func TestCreatorIDLister_Do_InputCreatorIDsSkipsAutomaticListing(t *testing.T) {
	transport := &staticResponseTransport{responses: map[string]string{}}
	lister := CreatorIDLister{OfficialAPIClient: newStaticOfficialAPIClient(transport)}

	creatorIDs, err := lister.Do(context.Background(), &CreatorIDListerDoInput{
		InputCreatorIDs:   []string{"first", "second"},
		IncludeSupporting: true,
		IncludeFollowing:  true,
		IgnoreCreatorIDs:  []string{"second"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"first"}, creatorIDs)
	assert.Empty(t, transport.requestURLs())
}

func TestCreatorIDLister_Do_IgnoresAutomaticallyListedCreatorIDs(t *testing.T) {
	transport := &staticResponseTransport{responses: map[string]string{
		"https://api.fanbox.cc/plan.listSupporting":   listSupportingCurrentResponse,
		"https://api.fanbox.cc/creator.listFollowing": listFollowingCurrentResponse,
	}}
	lister := CreatorIDLister{OfficialAPIClient: newStaticOfficialAPIClient(transport)}

	creatorIDs, err := lister.Do(context.Background(), &CreatorIDListerDoInput{
		IncludeSupporting: true,
		IncludeFollowing:  true,
		IgnoreCreatorIDs:  []string{"shared-creator"},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"supported-creator", "followed-creator"}, creatorIDs)
}

func TestCreatorIDLister_Do_InvalidResponseBodies(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		body       string
		input      CreatorIDListerDoInput
		errorMatch string
	}{
		{
			name:       "invalid supporting plans",
			url:        "https://api.fanbox.cc/plan.listSupporting",
			body:       `{"body":{"plans":{}}}`,
			input:      CreatorIDListerDoInput{IncludeSupporting: true},
			errorMatch: "list supporting plans",
		},
		{
			name:       "invalid following creators",
			url:        "https://api.fanbox.cc/creator.listFollowing",
			body:       `{"body":{"creators":{}}}`,
			input:      CreatorIDListerDoInput{IncludeFollowing: true},
			errorMatch: "list following creators",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := &staticResponseTransport{responses: map[string]string{tt.url: tt.body}}
			lister := CreatorIDLister{OfficialAPIClient: newStaticOfficialAPIClient(transport)}
			creatorIDs, err := lister.Do(context.Background(), &tt.input)
			require.ErrorContains(t, err, tt.errorMatch)
			assert.Nil(t, creatorIDs)
		})
	}
}

type staticResponseTransport struct {
	mu        sync.Mutex
	responses map[string]string
	requests  []string
}

func (t *staticResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	url := req.URL.String()
	t.requests = append(t.requests, url)
	body, ok := t.responses[url]
	if !ok {
		return nil, fmt.Errorf("unexpected request URL %q", url)
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     http.StatusText(http.StatusOK),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func (t *staticResponseTransport) requestURLs() []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	return append([]string(nil), t.requests...)
}

func newStaticOfficialAPIClient(transport http.RoundTripper) *OfficialAPIClient {
	httpClient := retryablehttp.NewClient()
	httpClient.HTTPClient.Transport = transport
	httpClient.RetryMax = 0

	return &OfficialAPIClient{HTTPClient: httpClient}
}
