package media

import (
	"testing"
)

// TestParseTimestamp_Seconds verifies pure second values (e.g., ?t=120).
func TestParseTimestamp_Seconds(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantSec  int
		wantOK   bool
	}{
		{
			name:    "?t=120 seconds",
			url:     "https://youtube.com/watch?v=xxx&t=120",
			wantSec: 120,
			wantOK:  true,
		},
		{
			name:    "&t=120 seconds (ampersand variant)",
			url:     "https://youtube.com/watch?v=xxx&t=120",
			wantSec: 120,
			wantOK:  true,
		},
		{
			name:    "t=0 edge case",
			url:     "https://youtube.com/watch?v=abc&t=0",
			wantSec: 0,
			wantOK:  true,
		},
		{
			name:    "t=45 among other params",
			url:     "https://youtube.com/watch?v=abc&t=45&feature=share",
			wantSec: 45,
			wantOK:  true,
		},
		{
			name:    "t=10 at end of URL",
			url:     "https://youtu.be/abc?t=10",
			wantSec: 10,
			wantOK:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSec, gotOK := ParseTimestamp(tt.url)
			if gotOK != tt.wantOK {
				t.Errorf("ParseTimestamp(%q) ok = %v, want %v", tt.url, gotOK, tt.wantOK)
			}
			if gotSec != tt.wantSec {
				t.Errorf("ParseTimestamp(%q) sec = %d, want %d", tt.url, gotSec, tt.wantSec)
			}
		})
	}
}

// TestParseTimestamp_DurationFormats verifies Go-compatible duration strings
// like 1m30s (90s) and 2h5m (7500s) in YouTube t= parameters.
func TestParseTimestamp_DurationFormats(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantSec int
		wantOK  bool
	}{
		{
			name:    "1m30s → 90 seconds",
			url:     "https://youtube.com/watch?v=abc&t=1m30s",
			wantSec: 90,
			wantOK:  true,
		},
		{
			name:    "2h5m → 7500 seconds",
			url:     "https://youtube.com/watch?v=abc&t=2h5m",
			wantSec: 7500,
			wantOK:  true,
		},
		{
			name:    "1h30m → 5400 seconds",
			url:     "https://youtube.com/watch?v=abc&t=1h30m",
			wantSec: 5400,
			wantOK:  true,
		},
		{
			name:    "5m → 300 seconds",
			url:     "https://youtube.com/watch?v=abc&t=5m",
			wantSec: 300,
			wantOK:  true,
		},
		{
			name:    "1h → 3600 seconds",
			url:     "https://youtube.com/watch?v=abc&t=1h",
			wantSec: 3600,
			wantOK:  true,
		},
		{
			name:    "bare seconds with s suffix: 90s → 90 seconds",
			url:     "https://youtube.com/watch?v=abc&t=90s",
			wantSec: 90,
			wantOK:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSec, gotOK := ParseTimestamp(tt.url)
			if gotOK != tt.wantOK {
				t.Errorf("ParseTimestamp(%q) ok = %v, want %v", tt.url, gotOK, tt.wantOK)
			}
			if gotSec != tt.wantSec {
				t.Errorf("ParseTimestamp(%q) sec = %d, want %d", tt.url, gotSec, tt.wantSec)
			}
		})
	}
}

// TestParseTimestamp_NoTimestamp verifies that URLs without a t= parameter
// return ok=false (spec R3.5 + S2: skip extraction on no timestamp).
func TestParseTimestamp_NoTimestamp(t *testing.T) {
	tests := []string{
		"https://youtube.com/watch?v=xxx",
		"https://youtu.be/abc",
		"https://www.youtube.com/embed/abc",
		"https://youtube.com/watch?v=xxx&list=PLxyz",
		"https://youtube.com/watch?v=xxx&t",    // t= without value (edge case)
		"https://youtube.com/watch?v=xxx&t=",    // t= empty value
	}

	for _, url := range tests {
		t.Run(url, func(t *testing.T) {
			_, gotOK := ParseTimestamp(url)
			if gotOK {
				t.Errorf("ParseTimestamp(%q) ok = true, want false (no valid timestamp)", url)
			}
		})
	}
}

// TestParseTimestamp_Malformed verifies that malformed or non-numeric
// t= values return ok=false.
func TestParseTimestamp_Malformed(t *testing.T) {
	tests := []string{
		"https://youtube.com/watch?v=xxx&t=notanumber",
		"https://youtube.com/watch?v=xxx&t=12abc",
		"https://youtube.com/watch?v=xxx&t=12.34",     // float, not int seconds
		"https://youtube.com/watch?v=xxx&t=-5",          // negative
		"https://youtube.com/watch?v=xxx&t=12h3",        // invalid format (2h3m?)
		"https://youtube.com/watch?v=xxx&t=1h30x",       // unrecognized unit
	}

	for _, url := range tests {
		t.Run(url, func(t *testing.T) {
			_, gotOK := ParseTimestamp(url)
			if gotOK {
				t.Errorf("ParseTimestamp(%q) ok = true, want false (malformed timestamp)", url)
			}
		})
	}
}

// TestParseTimestamp_NonYouTubeURL verifies that URLs without youtube/tubitv
// domains still parse correctly if they contain a t= parameter —
// ParseTimestamp is domain-agnostic (only parses the URL query).
func TestParseTimestamp_NonYouTubeURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantSec int
		wantOK  bool
	}{
		{
			name:    "generic URL with t=60",
			url:     "https://example.com/video?t=60",
			wantSec: 60,
			wantOK:  true,
		},
		{
			name:    "vimeo-like with timestamp",
			url:     "https://vimeo.com/123?t=30",
			wantSec: 30,
			wantOK:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSec, gotOK := ParseTimestamp(tt.url)
			if gotOK != tt.wantOK {
				t.Errorf("ParseTimestamp(%q) ok = %v, want %v", tt.url, gotOK, tt.wantOK)
			}
			if gotSec != tt.wantSec {
				t.Errorf("ParseTimestamp(%q) sec = %d, want %d", tt.url, gotSec, tt.wantSec)
			}
		})
	}
}
