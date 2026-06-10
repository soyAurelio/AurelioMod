// Package secrets provides a pluggable abstraction for secret retrieval.
package secrets

import (
	"fmt"
	"os"
)

// Getter abstracts secret retrieval from any source (env, Doppler, Vault).
type Getter interface {
	Get(key string) (string, bool)
	MustGet(key string) string
}

// EnvGetter reads secrets from OS environment variables.
type EnvGetter struct{}

func NewEnv() *EnvGetter { return &EnvGetter{} }

func (*EnvGetter) Get(key string) (string, bool) {
	v, ok := os.LookupEnv(key)
	return v, ok
}

func (e *EnvGetter) MustGet(key string) string {
	v, ok := e.Get(key)
	if !ok {
		panic(fmt.Sprintf("secrets: required key %q not found", key))
	}
	return v
}
