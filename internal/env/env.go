// Package env provides shared environment variable helpers
// used across cmd/* entrypoints.
package env

import "os"

// Get returns the env var value or fallback if unset.
func Get(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
