// Trusted video domains and partial scan policy for SigLIP2 video analysis.
//
// Trusted domains are content platforms where we trust the source origin
// but NOT the content itself. A YouTube URL at a specific timestamp may
// point to explicitly flagged material, so partial scanning is mandatory.
// ALLOW is never emitted without having analyzed at least ScanSeconds seconds.
//
// Partial scan policy:
//   - free tier: scan minimum seconds → if clean, log for audit, don't notify user
//   - pro tier: scan minimum seconds → if clean, allow
//   - enterprise tier: always full scan (no partial)

package pipeline

import (
	"net/url"
	"strings"
)

// TrustedVideoDomain defines a video domain and its minimum scan policy.
type TrustedVideoDomain struct {
	Domain               string
	ScanSeconds          int // minimum seconds to always scan
	FullScanThresholdSec int // if video is shorter than this, scan fully
}

// DefaultTrustedVideoDomains lists domains where we trust the origin but
// NOT the content. Partial scan is mandatory: never emit ALLOW without
// analyzing at least ScanSeconds.
var DefaultTrustedVideoDomains = []TrustedVideoDomain{
	{Domain: "youtube.com", ScanSeconds: 30, FullScanThresholdSec: 120},
	{Domain: "youtu.be", ScanSeconds: 30, FullScanThresholdSec: 120},
	{Domain: "vimeo.com", ScanSeconds: 30, FullScanThresholdSec: 120},
	{Domain: "twitch.tv", ScanSeconds: 30, FullScanThresholdSec: 120},
	{Domain: "clips.twitch.tv", ScanSeconds: 30, FullScanThresholdSec: 120},
	{Domain: "media.discordapp.net", ScanSeconds: 30, FullScanThresholdSec: 120},
	{Domain: "cdn.discordapp.com", ScanSeconds: 30, FullScanThresholdSec: 120},
}

// TrustedCDNDomains are CDN origins where WebRisk URL checks can be skipped.
// Content served from these domains is STILL analyzed with SigLIP2.
var TrustedCDNDomains = []string{
	"cdn.discordapp.com",
	"media.discordapp.net",
	"images-ext-1.discordapp.net",
	"images-ext-2.discordapp.net",
}

// FindTrustedVideo returns the scan policy for a trusted video domain,
// or nil if the domain is unknown (requires WebRisk check).
func FindTrustedVideo(rawURL string) *TrustedVideoDomain {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")

	for i := range DefaultTrustedVideoDomains {
		if host == DefaultTrustedVideoDomains[i].Domain ||
			strings.HasSuffix(host, "."+DefaultTrustedVideoDomains[i].Domain) {
			return &DefaultTrustedVideoDomains[i]
		}
	}
	return nil
}

// IsTrustedCDN returns true if the URL belongs to a known CDN where
// WebRisk checks can be skipped. Content is still analyzed.
func IsTrustedCDN(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	for _, cdn := range TrustedCDNDomains {
		if host == cdn || strings.HasSuffix(host, "."+cdn) {
			return true
		}
	}
	return false
}
