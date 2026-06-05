// Package testutil provides reusable testcontainers-based helpers for
// integration tests. Each container is started once per test run via
// sync.Once and reused across all tests in the package that calls it.
//
// All helpers are skippable: if Docker is not available, they return
// a graceful error that callers should handle with t.Skip().
package testutil

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// --- singleton state (one container per type per test binary) ---

var (
	onceDF    sync.Once
	onceNATS  sync.Once
	onceWV    sync.Once

	dragonflyAddr string
	dragonflyErr  error

	natsAddr string
	natsErr  error

	weaviateURL string
	weaviateErr error

	mu sync.Mutex // guards rdb/nc fields
)

// StartDragonfly starts a DragonflyDB v1.38 container once per test binary
// and returns a connected go-redis client. The container listens on a
// random host port to avoid conflicts.
//
// If Docker is unavailable, the function returns an error — callers should
// handle this gracefully with t.Skip() for integration tests.
func StartDragonfly(ctx context.Context) (*redis.Client, error) {
	onceDF.Do(func() {
		if !dockerAvailable() {
			dragonflyErr = fmt.Errorf("Docker not available")
			return
		}

		req := testcontainers.ContainerRequest{
			Image:        "ghcr.io/dragonflydb/dragonfly:v1.38.0",
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor:   wait.ForLog("I00000000 00000000 server:1] Ready to accept connections").WithStartupTimeout(30 * time.Second),
		}

		ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			dragonflyErr = fmt.Errorf("StartDragonfly: container start: %w", err)
			return
		}

		host, err := ctr.Host(ctx)
		if err != nil {
			dragonflyErr = fmt.Errorf("StartDragonfly: host resolution: %w", err)
			return
		}
		port, err := ctr.MappedPort(ctx, "6379/tcp")
		if err != nil {
			dragonflyErr = fmt.Errorf("StartDragonfly: port mapping: %w", err)
			return
		}
		dragonflyAddr = fmt.Sprintf("%s:%s", host, port.Port())

		slog.Info("testutil: DragonflyDB container started", "addr", dragonflyAddr)
	})

	if dragonflyErr != nil {
		return nil, dragonflyErr
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:        dragonflyAddr,
		PoolSize:    10,
		DialTimeout: 3 * time.Second,
	})
	return rdb, nil
}

// StartNATS starts a NATS container once per test binary and returns
// a connected nats.Conn. The container is started without authentication.
func StartNATS(ctx context.Context) (*nats.Conn, error) {
	onceNATS.Do(func() {
		if !dockerAvailable() {
			natsErr = fmt.Errorf("Docker not available")
			return
		}

		req := testcontainers.ContainerRequest{
			Image:        "nats:2.11-alpine",
			ExposedPorts: []string{"4222/tcp"},
			Cmd:          []string{"-js"},
			WaitingFor:   wait.ForLog("Server is ready").WithStartupTimeout(15 * time.Second),
		}

		ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			natsErr = fmt.Errorf("StartNATS: container start: %w", err)
			return
		}

		host, err := ctr.Host(ctx)
		if err != nil {
			natsErr = fmt.Errorf("StartNATS: host resolution: %w", err)
			return
		}
		port, err := ctr.MappedPort(ctx, "4222/tcp")
		if err != nil {
			natsErr = fmt.Errorf("StartNATS: port mapping: %w", err)
			return
		}
		natsAddr = fmt.Sprintf("nats://%s:%s", host, port.Port())

		slog.Info("testutil: NATS container started", "addr", natsAddr)
	})

	if natsErr != nil {
		return nil, natsErr
	}

	nc, err := nats.Connect(natsAddr)
	if err != nil {
		return nil, fmt.Errorf("StartNATS: connect: %w", err)
	}
	return nc, nil
}

// StartWeaviate starts a Weaviate container once per test binary and returns
// the HTTP URL for the Weaviate REST API.
func StartWeaviate(ctx context.Context) (string, error) {
	onceWV.Do(func() {
		if !dockerAvailable() {
			weaviateErr = fmt.Errorf("Docker not available")
			return
		}

		req := testcontainers.ContainerRequest{
			Image:        "semitechnologies/weaviate:1.29.0",
			ExposedPorts: []string{"8080/tcp", "50051/tcp"},
			Env: map[string]string{
				"AUTHENTICATION_ANONYMOUS_ACCESS_ENABLED": "true",
				"PERSISTENCE_DATA_PATH":                   "/var/lib/weaviate",
				"QUERY_DEFAULTS_LIMIT":                    "20",
			},
			Cmd: []string{"--host", "0.0.0.0", "--scheme", "http", "--port", "8080"},
			WaitingFor: wait.ForAll(
				wait.ForLog("Startup complete").WithStartupTimeout(60*time.Second),
			),
		}

		ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			weaviateErr = fmt.Errorf("StartWeaviate: container start: %w", err)
			return
		}

		host, err := ctr.Host(ctx)
		if err != nil {
			weaviateErr = fmt.Errorf("StartWeaviate: host resolution: %w", err)
			return
		}
		port, err := ctr.MappedPort(ctx, "8080/tcp")
		if err != nil {
			weaviateErr = fmt.Errorf("StartWeaviate: port mapping: %w", err)
			return
		}
		weaviateURL = fmt.Sprintf("http://%s:%s", host, port.Port())

		slog.Info("testutil: Weaviate container started", "url", weaviateURL)
	})

	if weaviateErr != nil {
		return "", weaviateErr
	}

	return weaviateURL, nil
}

// dockerAvailable checks if Docker is reachable via the client CLI.
// This is a lightweight check — no container is started.
func dockerAvailable() bool {
	cmd := exec.Command("docker", "info")
	return cmd.Run() == nil
}

// stringsHasPrefix is a thin wrapper for testing URL prefixes.
func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
