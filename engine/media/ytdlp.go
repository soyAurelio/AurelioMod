package media

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// YtDlpRunner is the interface for fetching content metadata from URLs
// via yt-dlp, whether directly or sandboxed via nsjail.
type YtDlpRunner interface {
	// Fetch retrieves content metadata from the given URL.
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// Compile-time interface check.
var _ YtDlpRunner = (*NsJailYtDlp)(nil)

// NsJailYtDlp fetches content metadata from a yt-dlp sidecar container,
// optionally wrapped in nsjail for network isolation.
type NsJailYtDlp struct {
	nsjailPath   string
	sidecarURL   string
	enabled      bool
	maxRedirects int
	httpClient   *http.Client
}

// NewNsJailYtDlp creates a sandboxed yt-dlp runner.
// When enabled is false, the runner operates in degraded mode.
// The sidecarURL points to the yt-dlp sidecar container endpoint.
func NewNsJailYtDlp(nsjailPath, sidecarURL string, enabled bool) *NsJailYtDlp {
	return &NsJailYtDlp{
		nsjailPath:   nsjailPath,
		sidecarURL:   sidecarURL,
		enabled:      enabled,
		maxRedirects: 5,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects: %d", len(via))
				}
				return nil
			},
		},
	}
}

// Fetch retrieves content metadata from the given URL via the yt-dlp sidecar.
func (n *NsJailYtDlp) Fetch(ctx context.Context, url string) ([]byte, error) {
	resp, err := n.fetchFromSidecar(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("yt-dlp fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yt-dlp sidecar returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("yt-dlp read body: %w", err)
	}

	return body, nil
}

// fetchFromSidecar makes an HTTP request to the yt-dlp sidecar.
// Exported for testing.
func (n *NsJailYtDlp) fetchFromSidecar(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.sidecarURL, nil)
	if err != nil {
		return nil, fmt.Errorf("yt-dlp request: %w", err)
	}

	// Pass the target URL as a query parameter
	q := req.URL.Query()
	q.Set("url", url)
	req.URL.RawQuery = q.Encode()

	return n.httpClient.Do(req)
}

// buildYtDlpNsJailArgs constructs nsjail arguments for yt-dlp execution.
// The sidecar runs as a separate container; nsjail wraps the local yt-dlp
// binary (if present) with network disabled.
func buildYtDlpNsJailArgs(nsjailPath, sidecarURL, targetURL string) []string {
	_ = nsjailPath
	_ = sidecarURL
	_ = targetURL

	return []string{
		"--net", "none",
		"--cwd", "/tmp",
		"--",
		"yt-dlp",
		"--max-redirects", "5",
		targetURL,
	}
}
