// Package secrets provides a pluggable abstraction for secret retrieval.
package secrets

import (
	"errors"
	"os"
)

// ErrSecretNotFound is returned when a required secret key is missing.
var ErrSecretNotFound = errors.New("secret not found")

// Getter abstracts secret retrieval from any source (env, Doppler, Vault).
type Getter interface {
	Get(key string) (string, bool)
}

// EnvGetter reads secrets from OS environment variables.
type EnvGetter struct{}

func NewEnv() *EnvGetter { return &EnvGetter{} }

func (*EnvGetter) Get(key string) (string, bool) {
	v, ok := os.LookupEnv(key)
	return v, ok
}
